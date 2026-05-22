# 自定义 v2rayA 部署和更新记录

这个 fork 的自定义改动统一维护在分支：

```text
custom/node-country-health
```

`main` 尽量保持和官方 `upstream/main` 一致，后续官方更新时只 rebase/merge 这个自定义分支。

## 服务器构建

服务器代码目录：

```bash
cd /opt/v2rayA
git switch custom/node-country-health
git pull --ff-only
git submodule update --init --recursive
```

新服务器先安装构建依赖：

```bash
sudo snap install go --classic
sudo npm install -g yarn
```

注意不要执行：

```bash
apt install cmdtest
```

Ubuntu 里 `cmdtest` 会占用 `yarn` 命令，但它不是前端构建需要的 Yarn。

构建：

```bash
cd /opt/v2rayA
./build.sh
```

构建成功后会生成：

```text
/opt/v2rayA/v2raya
/opt/v2rayA/v2raya_core
```

## 复用 Geo 资源文件

如果 snap 版已经下载过 `geoip.dat`、`geosite.dat`，可以直接复制给自定义版用，避免重新下载太慢：

```bash
mkdir -p /etc/v2raya/geoip
cp /var/snap/v2raya/49/geoip/geoip.dat /etc/v2raya/geoip/
cp /var/snap/v2raya/49/geoip/geosite.dat /etc/v2raya/geoip/
cp /var/snap/v2raya/49/geoip/LoyalsoldierSite.dat /etc/v2raya/geoip/
```

如果 snap 版本目录变了，把 `/var/snap/v2raya/49/geoip` 换成新的版本目录即可。

## systemd 服务

如果 snap 服务还在运行，先停掉，避免端口冲突：

```bash
snap stop v2raya
```

创建服务文件：

```bash
nano /etc/systemd/system/v2raya-custom.service
```

内容：

```ini
[Unit]
Description=v2rayA Custom
After=network.target

[Service]
Type=simple
ExecStart=/opt/v2rayA/v2raya
Restart=on-failure
RestartSec=5s

[Install]
WantedBy=multi-user.target
```

用 override 加环境变量：

```bash
systemctl edit v2raya-custom
```

写在 `### Edits below this comment will be discarded` 上方：

```ini
[Service]
Environment=V2RAYA_V2RAY_BIN=/opt/v2rayA/v2raya_core
Environment=V2RAYA_CORE_TYPE=v2raya_core
Environment=V2RAYA_V2RAY_ASSETSDIR=/etc/v2raya/geoip
Environment=V2RAYA_LOG_FILE=/var/log/v2raya/v2raya.log
```

启动服务：

```bash
mkdir -p /var/log/v2raya
systemctl daemon-reload
systemctl enable --now v2raya-custom
systemctl restart v2raya-custom
```

确认环境变量是否生效：

```bash
systemctl show v2raya-custom -p Environment
```

## 日志

看 systemd 日志：

```bash
journalctl -u v2raya-custom -f
```

看文件日志：

```bash
tail -f /var/log/v2raya/v2raya.log
```

看最近 100 行：

```bash
tail -n 100 /var/log/v2raya/v2raya.log
```

查询节点健康检查日志：

```bash
grep -i "HealthCheck" /var/log/v2raya/v2raya.log
tail -f /var/log/v2raya/v2raya.log | grep -i "HealthCheck"
```

查询订阅更新错误：

```bash
grep -Ei "AutoUpdate|UpdateSubscription|Failed to update subscription|TLS handshake timeout" /var/log/v2raya/v2raya.log
```

分页查看日志：

```bash
less /var/log/v2raya/v2raya.log
```

`less` 里按 `/` 搜索，按 `q` 退出。

## 当前自定义健康检查规则

当前固定规则：

```text
检查间隔：30 分钟
失败阈值：连续 2 次不可用
恢复冷却：同一个失败节点恢复后冷却 6 小时
```

健康检查读取 core observatory 的最近状态，不会每次单独启动临时 core。

以下情况会跳过检查：

```text
core 没运行
没有已连接节点
还没有 observatory 状态快照
```

## 代码变更后的部署

```bash
cd /opt/v2rayA
git switch custom/node-country-health
git pull --ff-only
./build.sh
systemctl restart v2raya-custom
tail -n 100 /var/log/v2raya/v2raya.log
```

## 官方更新后的合并流程

先确认 remote：

```bash
git remote -v
git remote add upstream https://github.com/v2rayA/v2rayA.git 2>/dev/null || true
```

把官方更新合并到自定义分支：

```bash
cd /opt/v2rayA
git fetch upstream
git switch custom/node-country-health
git rebase upstream/main
```

如果有冲突，解决冲突后继续：

```bash
git status
git add <已解决的文件>
git rebase --continue
```

验证：

```bash
cd /opt/v2rayA/service
go test ./db ./db/configure ./server/service ./core/touch ./core/v2ray
cd /opt/v2rayA
./build.sh
systemctl restart v2raya-custom
```

推送 rebase 后的分支：

```bash
git push --force-with-lease
```

## 保持 main 干净

`main` 用来跟官方保持一致：

```bash
git switch main
git fetch upstream
git reset --hard upstream/main
git push --force-with-lease origin main
```

自己的改动只放在：

```text
custom/node-country-health
```
