# AIFAR HTTPS Ingress

这是一个可独立复制到 AIFAR Linux 服务器运行的 Docker HTTPS 入口模块。它不修改现有 AIFAR Runtime 容器：

- `https://aifar.local/` 转发至宿主机 AIFAR Web listener `127.0.0.1:8080`；
- `/api/` 与 `/im/ws` 转发至宿主机 Gateway listener `127.0.0.1:38000`；
- 使用 `nginx:stable-alpine`、Linux host network 和只读配置/证书挂载；
- Docker 使用 `unless-stopped`，同时提供可选 systemd 开机自启动。

## 重要安全说明

随模块提供的证书是为 `aifar.local` 生成的 self-signed（自签名）启动证书。它只用于快速验证 HTTPS，浏览器会提示不受信任。该私钥不是生产秘密，不应在生产环境继续使用。

正式上线前，把以下文件替换为与实际域名匹配、来源可信且相互匹配的正式证书：

- `tls/fullchain.pem`
- `tls/privkey.pem`

同时把 `conf.d/aifar.conf` 中两处 `aifar.local` 改为实际域名。

## 前置条件

1. Linux 服务器已经安装并启动 Docker。
2. 本机已有 `nginx:stable-alpine` 镜像；离线环境可先用 `docker load -i nginx-stable-alpine.tar` 导入。
3. AIFAR Agent 正在监听 `8080` 和 `38000`。
4. 宿主机 `80`、`443` 没有被其他服务占用。

检查：

```bash
docker image inspect nginx:stable-alpine
ss -lntp | grep -E ':8080|:38000|:80|:443'
```

## 直接启动

把整个 `aifar-https-ingress` 目录复制到服务器，例如 `/aifar/apps/aifar-https-ingress`：

```bash
cd /aifar/apps/aifar-https-ingress
chmod 0755 *.sh
./start.sh
./status.sh
```

如果没有 DNS，在访问电脑的 hosts 文件中添加：

```text
服务器IP aifar.local
```

访问：

```text
https://aifar.local/
```

## 启停与重新加载

```bash
./start.sh
./status.sh
./reload.sh
./stop.sh
```

修改 Nginx 配置或替换证书后运行：

```bash
./reload.sh
```

## 安装 systemd 自启动

模块必须先放到最终目录，再安装 unit，因为 unit 会记录当前绝对路径：

```bash
cd /aifar/apps/aifar-https-ingress
sudo ./install-systemd.sh
```

查看状态和日志：

```bash
systemctl status aifar-https-ingress.service
docker logs aifar-https-ingress
```

卸载 systemd unit：

```bash
sudo ./uninstall-systemd.sh
```

这不会删除模块目录、配置或证书。

## 防火墙

`install-systemd.sh` 默认会在 firewalld 中默认路由网卡所属的 zone（无法识别时使用默认 zone）同时、幂等地放行 HTTP/HTTPS（运行时和永久配置），且不会 reload、删除或覆盖已有规则。若自动识别结果不符合实际入口网卡，可在 `config.env` 中指定：

```bash
AIFAR_HTTPS_FIREWALL_ZONE=public
```

若防火墙由外部系统统一管理，可设置 `AIFAR_HTTPS_CONFIGURE_FIREWALL=0` 关闭自动配置；此时需自行放行 TCP `80`、`443`。卸载脚本不会关闭 HTTP/HTTPS，避免误删安装前已有或其他服务共用的规则。

建议在公网安全组或防火墙中禁止直接访问 `8080`、`38000`，只开放 `80`、`443`。

## 验证

```bash
curl -vk https://aifar.local/
curl -vk https://aifar.local/api/
docker logs aifar-https-ingress
```

重点验证登录、API 请求和 `/im/ws` WebSocket。
