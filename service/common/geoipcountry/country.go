package geoipcountry

import (
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/v2rayA/v2rayA/common/resolv"
	"github.com/v2rayA/v2rayA/pkg/util/log"
)

const (
	resolveRefreshAfter = 10 * time.Minute
	failedCacheDuration = 30 * time.Minute
	queryTimeout        = 2 * time.Second
)

type hostEntry struct {
	IP          string
	CountryCode string
	LastResolve time.Time
	FailUntil   time.Time
	InFlight    bool
}

type countryResponse struct {
	Country string `json:"country"`
}

var (
	mu       sync.Mutex
	hosts    = make(map[string]*hostEntry)
	ipCache  = make(map[string]string)
	ipFailed = make(map[string]time.Time)
	limit    = make(chan struct{}, 2)
	client   = &http.Client{Timeout: queryTimeout}
)

func CountryCodeForHost(host string) string {
	host = normalizeHost(host)
	if host == "" {
		return ""
	}
	now := time.Now()
	mu.Lock()
	entry := hosts[host]
	if entry == nil {
		entry = &hostEntry{InFlight: true}
		hosts[host] = entry
		go refresh(host)
		mu.Unlock()
		return ""
	}
	countryCode := entry.CountryCode
	shouldRefresh := !entry.InFlight && now.After(entry.FailUntil) && now.Sub(entry.LastResolve) > resolveRefreshAfter
	if shouldRefresh {
		entry.InFlight = true
		go refresh(host)
	}
	mu.Unlock()
	return countryCode
}

func normalizeHost(host string) string {
	host = strings.TrimSpace(host)
	host = strings.Trim(host, "[]")
	if strings.Contains(host, ":") {
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
	}
	return strings.ToLower(strings.Trim(host, "[]"))
}

func refresh(host string) {
	defer func() {
		mu.Lock()
		if entry := hosts[host]; entry != nil {
			entry.InFlight = false
		}
		mu.Unlock()
	}()

	ip := resolvePublicIP(host)
	if ip == "" {
		markHostFailure(host)
		return
	}

	mu.Lock()
	entry := hosts[host]
	if entry != nil && entry.IP == ip && entry.CountryCode != "" {
		entry.LastResolve = time.Now()
		entry.FailUntil = time.Time{}
		mu.Unlock()
		return
	}
	if countryCode := ipCache[ip]; countryCode != "" {
		if entry != nil {
			entry.IP = ip
			entry.CountryCode = countryCode
			entry.LastResolve = time.Now()
			entry.FailUntil = time.Time{}
		}
		mu.Unlock()
		return
	}
	if until := ipFailed[ip]; time.Now().Before(until) {
		if entry != nil {
			entry.IP = ip
			entry.FailUntil = until
		}
		mu.Unlock()
		return
	}
	mu.Unlock()

	countryCode := queryCountry(ip)
	mu.Lock()
	defer mu.Unlock()
	entry = hosts[host]
	if countryCode == "" {
		until := time.Now().Add(failedCacheDuration)
		ipFailed[ip] = until
		if entry != nil {
			entry.IP = ip
			entry.FailUntil = until
		}
		return
	}
	ipCache[ip] = countryCode
	delete(ipFailed, ip)
	if entry != nil {
		entry.IP = ip
		entry.CountryCode = countryCode
		entry.LastResolve = time.Now()
		entry.FailUntil = time.Time{}
	}
}

func resolvePublicIP(host string) string {
	if ip := net.ParseIP(host); ip != nil {
		if isPublicIP(ip) {
			return ip.String()
		}
		return ""
	}
	addrs, err := resolv.LookupHost(host)
	if err != nil {
		log.Debug("country: resolve %s: %v", host, err)
		return ""
	}
	for _, addr := range addrs {
		ip := net.ParseIP(addr)
		if isPublicIP(ip) {
			return ip.String()
		}
	}
	return ""
}

func isPublicIP(ip net.IP) bool {
	return ip != nil &&
		!ip.IsUnspecified() &&
		!ip.IsLoopback() &&
		!ip.IsPrivate() &&
		!ip.IsLinkLocalUnicast() &&
		!ip.IsLinkLocalMulticast() &&
		!ip.IsMulticast()
}

func markHostFailure(host string) {
	mu.Lock()
	defer mu.Unlock()
	if entry := hosts[host]; entry != nil {
		entry.FailUntil = time.Now().Add(failedCacheDuration)
	}
}

func queryCountry(ip string) string {
	limit <- struct{}{}
	defer func() { <-limit }()

	resp, err := client.Get("https://api.country.is/" + ip)
	if err != nil {
		log.Debug("country: query %s: %v", ip, err)
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		log.Debug("country: query %s status %s", ip, resp.Status)
		return ""
	}
	var data countryResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		log.Debug("country: decode %s: %v", ip, err)
		return ""
	}
	country := strings.ToUpper(strings.TrimSpace(data.Country))
	if len(country) != 2 {
		return ""
	}
	return country
}
