# winroute

Windows 11 后台策略路由工具。同时连接两张网络（公司网 + 家用 CPE 路由器网，网线或 WiFi 均可）时：

- **目的地是 `10.0.0.0/8`（公司网段）的流量走公司网络**；
- **其余所有流量走你的路由器网络**；
- 可在 `config.json` 里**手动指定额外 IP/网段走哪边**；
- 无界面，后台常驻；任一网络断开/重连都会在几秒内自动重算路由。

## 工作原理

每隔 `poll_seconds` 秒枚举一次网卡（`GetAdaptersAddresses`）：

- **识别公司网卡**：哪张网卡拿到的 IPv4 地址落在 `company_detect_cidr`（默认 `10.0.0.0/8`）里，就是公司网；也可用 `company_interface_hint` 按网卡名指定。
- **识别路由器网卡**：剩下的、带默认网关的网卡里 metric 最低的那张（或用 `router_interface_hint` 指定）。
- 然后把期望的路由“对账”进系统路由表：
  - 公司网段（`company_cidrs` + `rules` 里 `target=company`）→ 指向公司网关；
  - `rules` 里 `target=router` 的网段 → 指向路由器网关；
  - 当**两张网都在线**且 `force_default_to_router=true` 时，安装 `0.0.0.0/1` 和 `128.0.0.0/1` 两条指向路由器网关的路由。它们比公司网可能下发的默认路由（`0.0.0.0/0`）更具体，所以默认流量会走路由器；而 `10.0.0.0/8` 比 `/1` 更具体，所以公司流量仍走公司网。

只连一张网时不做任何多余改动；程序退出时会撤销自己加过的所有路由。路由都是**非持久**的（重启即清空，服务会重新装），不会污染系统。

## 编译

需要 Go 1.21+。

```sh
# 在 Windows 上
go build -o winroute.exe .

# 或在 Linux/macOS 上交叉编译
GOOS=windows GOARCH=amd64 go build -o winroute.exe .
```

## 配置

把 `config.example.json` 复制成 `config.json`（与 exe 同目录）。首次运行若不存在会自动生成一份默认配置。字段见 `config.example.json`：

- `company_cidrs`：始终走公司网的网段，默认 `["10.0.0.0/8"]`。
- `company_detect_cidr`：用于识别哪张网卡是公司网，默认 `10.0.0.0/8`。
- `company_interface_hint` / `router_interface_hint`：按网卡名（友好名，含子串即可）强制指定，优先级高于上面的自动识别。公司网不是 10 开头时用这个。
- `rules`：额外覆盖，`target` 取 `company` 或 `router`。`dest` 字段支持三种写法：
  - 网段 `"172.16.0.0/12"`；
  - 裸 IP `"8.8.8.8"`（按 `/32` 处理）；
  - **域名** `"oa.company.com"`：用系统 DNS 解析出所有 A 记录，给每个 IP 装一条 `/32` 路由，解析结果变化时自动增删。
- `dns_refresh_seconds`：域名重解析间隔秒数，默认 60。解析失败时保留上次结果，不会误删路由。
- `force_default_to_router`：两网都在线时是否把默认路由抢到路由器，默认 `true`。
- `poll_seconds`：轮询间隔秒数，默认 5。

## 运行 / 安装为后台服务

修改路由表需要管理员权限。最简单的常驻方式是注册成开机自启的计划任务（以 SYSTEM 身份运行）：

```powershell
# 以管理员身份打开 PowerShell，cd 到 winroute.exe 所在目录
.\install.ps1            # 安装并启动
.\install.ps1 -Uninstall # 卸载
```

日志写到同目录 `winroute.log`。

### 手动调试

```powershell
# 用管理员 PowerShell 直接前台运行，观察日志
.\winroute.exe                 # 前台常驻
.\winroute.exe -once           # 只对账一次后退出（看一眼会装哪些路由）
.\winroute.exe -config C:\path\config.json -log C:\path\winroute.log
```

## 命令行参数

- `-config <path>`：配置文件路径，默认 exe 同目录的 `config.json`。
- `-log <path>`：日志文件，默认输出到 stdout。
- `-once`：执行一次对账后退出，便于调试。
