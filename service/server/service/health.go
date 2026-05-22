package service

import (
	"fmt"
	"sync"
	"time"

	"github.com/v2rayA/v2rayA/core/v2ray"
	"github.com/v2rayA/v2rayA/db/configure"
	"github.com/v2rayA/v2rayA/pkg/util/log"
)

const (
	healthCheckInterval = 30 * time.Minute
	healthFailThreshold = 2
	healthRecoverCool   = 6 * time.Hour
)

type healthState struct {
	Failures  int
	CoolUntil time.Time
}

var (
	healthMu     sync.Mutex
	healthStates = make(map[string]*healthState)
)

func StartSelectedNodeHealthCheck() {
	log.Info("[HealthCheck] Selected node health check started: interval=%s, failureThreshold=%d, recoveryCooldown=%s", healthCheckInterval, healthFailThreshold, healthRecoverCool)
	go func() {
		ticker := time.NewTicker(healthCheckInterval)
		defer ticker.Stop()
		for range ticker.C {
			checkSelectedNodeHealth()
		}
	}()
}

func checkSelectedNodeHealth() {
	log.Info("[HealthCheck] Checking selected node health")
	if !v2ray.ProcessManager.Running() {
		log.Info("[HealthCheck] Skip check: core is not running")
		return
	}
	connected := configure.GetConnectedServers()
	if connected == nil || connected.Len() == 0 {
		log.Info("[HealthCheck] Skip check: no connected servers")
		return
	}
	statuses := flattenOutboundStatus(v2ray.GetOutboundStatusSnapshot())
	if len(statuses) == 0 {
		log.Info("[HealthCheck] Skip check: no observatory status snapshot")
		return
	}
	log.Info("[HealthCheck] Checking %d observatory status entries for %d connected servers", len(statuses), connected.Len())
	now := time.Now()
	var toRecover []configure.Which
	for _, status := range statuses {
		if status.Which == nil {
			log.Info("[HealthCheck] Skip status without node identity: outboundTag=%s alive=%v", status.OutboundTag, status.Alive)
			continue
		}
		key := healthKey(*status.Which)
		healthMu.Lock()
		state := healthStates[key]
		if state == nil {
			state = &healthState{}
			healthStates[key] = state
		}
		if status.Alive {
			if state.Failures > 0 {
				log.Info("[HealthCheck] Node %s recovered by itself, reset failure count from %d", key, state.Failures)
			}
			state.Failures = 0
			healthMu.Unlock()
			continue
		}
		state.Failures++
		failures := state.Failures
		cooling := now.Before(state.CoolUntil)
		log.Info("[HealthCheck] Node %s unavailable: failures=%d/%d, cooling=%v", key, failures, healthFailThreshold, cooling)
		if failures >= healthFailThreshold && !cooling {
			state.CoolUntil = now.Add(healthRecoverCool)
			healthMu.Unlock()
			log.Info("[HealthCheck] Node %s reached failure threshold, scheduling recovery", key)
			toRecover = append(toRecover, *status.Which)
			continue
		}
		healthMu.Unlock()
	}
	if len(toRecover) == 0 {
		log.Info("[HealthCheck] Check finished: no recovery needed")
	} else {
		log.Info("[HealthCheck] Check finished: %d node(s) need recovery", len(toRecover))
	}
	recoverSelectedNodes(toRecover)
}

func flattenOutboundStatus(snapshot map[string][]v2ray.OutboundStatus) []v2ray.OutboundStatus {
	var statuses []v2ray.OutboundStatus
	for _, outboundStatuses := range snapshot {
		statuses = append(statuses, outboundStatuses...)
	}
	return statuses
}

func healthKey(which configure.Which) string {
	return fmt.Sprintf("%s/%d/%d/%s", which.TYPE, which.Sub, which.ID, which.Outbound)
}

func recoverSelectedNodes(whiches []configure.Which) {
	updatedSubscriptions := make(map[int]bool)
	for _, which := range whiches {
		recoverSelectedNode(which, updatedSubscriptions)
	}
}

func recoverSelectedNode(which configure.Which, updatedSubscriptions map[int]bool) {
	if which.TYPE != configure.SubscriptionServerType {
		log.Info("[HealthCheck] Node %s is unavailable, automatic recovery only applies to subscription nodes", healthKey(which))
		return
	}
	raw, err := which.LocateServerRaw()
	if err != nil {
		log.Warn("[HealthCheck] Failed to locate unhealthy node %s: %v", healthKey(which), err)
		return
	}
	if raw.ServerObj == nil {
		log.Warn("[HealthCheck] Unhealthy node %s has nil ServerObj", healthKey(which))
		return
	}
	name := raw.ServerObj.GetName()
	outbound := which.Outbound
	if outbound == "" {
		outbound = configure.DefaultOutboundName
	}
	if !updatedSubscriptions[which.Sub] {
		log.Info("[HealthCheck] Updating subscription %d to recover node %q in outbound %s", which.Sub, name, outbound)
		if err := UpdateSubscription(which.Sub, false); err != nil {
			log.Warn("[HealthCheck] Failed to update subscription %d for node %q: %v", which.Sub, name, err)
			return
		}
		updatedSubscriptions[which.Sub] = true
	}
	newID := findSubscriptionServerByName(which.Sub, name)
	if newID <= 0 {
		log.Warn("[HealthCheck] No node named %q found in subscription %d after update", name, which.Sub)
		return
	}
	replacement := configure.Which{
		TYPE:     configure.SubscriptionServerType,
		ID:       newID,
		Sub:      which.Sub,
		Outbound: outbound,
	}
	if replacement.EqualTo(which) {
		log.Info("[HealthCheck] Node %q remains at the same subscription index after update", name)
		return
	}
	if err := replaceOutboundMember(outbound, which, replacement); err != nil {
		log.Warn("[HealthCheck] Failed to replace node %q in outbound %s: %v", name, outbound, err)
		return
	}
	healthMu.Lock()
	delete(healthStates, healthKey(which))
	delete(healthStates, healthKey(replacement))
	healthMu.Unlock()
	log.Info("[HealthCheck] Re-selected node %q in subscription %d: %d -> %d", name, which.Sub, which.ID, newID)
}

func findSubscriptionServerByName(sub int, name string) int {
	subscription := configure.GetSubscription(sub)
	if subscription == nil {
		return 0
	}
	for i, server := range subscription.Servers {
		if server.ServerObj != nil && server.ServerObj.GetName() == name {
			return i + 1
		}
	}
	return 0
}

func replaceOutboundMember(outbound string, oldWhich configure.Which, newWhich configure.Which) error {
	current := configure.GetConnectedServersByOutbound(outbound)
	if current == nil {
		return fmt.Errorf("outbound %s has no connected servers", outbound)
	}
	next := make([]configure.Which, 0, current.Len())
	replaced := false
	for _, member := range current.Get() {
		if member.EqualTo(oldWhich) {
			next = append(next, newWhich)
			replaced = true
			continue
		}
		next = append(next, *member)
	}
	if !replaced {
		return fmt.Errorf("unhealthy node %s not found in outbound %s", healthKey(oldWhich), outbound)
	}
	return ReplaceOutboundConnections(outbound, next)
}
