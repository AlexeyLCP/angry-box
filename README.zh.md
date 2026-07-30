<div align="center">

# Angry-BOX

**完全自研的 SSH-only orchestrator / control plane。**

Angry-BOX 是从零开始编写的原创产品，不是 3x-ui、LucX-UI、x-ui 或任何其他面板的 fork。

所有管理均通过 SSH 完成，目标节点上只运行 **amnezia-box**（我们 fork 的 sing-box 1.14）内核 + 最小配置，无任何 agent。

🌐 **Languages / Языки:** [English](README.md) | [Русский](README.ru.md) | [简体中文](README.zh.md) | [فارسی](README.fa.md)

<p align="center">
  <a href="https://github.com/AlexeyLCP/angry-box/releases"><img src="https://img.shields.io/github/v/release/AlexeyLCP/angry-box" alt="Release"></a>
  <a href="https://golang.org"><img src="https://img.shields.io/github/go-mod/go-version/AlexeyLCP/angry-box" alt="Go Version"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-PolyForm%20Noncommercial-blue.svg" alt="License"></a>
  <a href="https://yoomoney.ru/to/41001989176429"><img src="https://img.shields.io/badge/donate-☕-yellow" alt="Donate"></a>
</p>

</div>

## 概览

**Angry-BOX** 是一个完全原创、自研的编排器（控制平面），用于构建和管理复杂的抗 DPI 代理基础设施。

它通过 SSH 驱动 **amnezia-box**（我们 fork 的 sing-box 1.14）内核，节点上零 agent。全部逻辑——链式组合、合并配置、基于角色的配置生成、节点健康追踪、回滚、UI 和部署——均从零编写。

## 功能

- **接管现有 VPN 服务器（takeover）：** 连接到运行现有 VPN（AWG / awg-quick、sing-box、Xray/3x-ui、MTProxy/telemt）的节点 → Angry-BOX 检测到它、发出警告，并在同意后安装 sing-box，**将现有配置转换为相同设置的 sing-box 配置**，禁用（但不删除）旧 VPN，启动 sing-box，如果 sing-box 启动失败则**自动回滚到旧 VPN**。
- **实时 QUIC 签名捕获：** 获取真实域名的 QUIC 轮廓（UDP→QUIC Initial with SNI=domain→捕获服务器响应），用作 AmneziaWG CPS I1-I5，使 DPI 看到的流量与该域名的真实 QUIC 不可区分。
- **导入现有 AmneziaWG 配置：** 通过 SSH 拉取运行中服务器的 AWG 接口 + peer 列表，并以**非破坏性**方式回填为节点的入站（仅占位符 — 从不覆盖操作员设定的密钥、端口或预设）。无需重新输入即可接管 AWG 节点。
- **自动化编排：** 无需手动编写复杂的 `sing-box` JSON 配置。Angry-BOX 通过 SSH 在几秒内生成、验证并部署配置。
- **高级混淆：** AmneziaWG（内核 + 平衡器）、VLESS REALITY+XHTTP max obfuscation、MTProxy/Telemt FakeTLS — 提供 4 个混淆级别（max/high/standard/minimal）和 45 个路由预设（Telegram/YouTube/Netflix/…）。TUIC 和 Hysteria2 **暂停**（QUIC/TLS 证书工作暂缓）。
- **多跳链：** 构建 2 节点或 3 节点代理链；AmneziaWG 既可作为客户端入口点（内核 awg-quick + sing-box bind_interface），也可作为节点间跳（带 amnezia 的用户态 wireguard endpoint — 修补后的二进制修复了之前导致内核模式 AWG 崩溃的上游 `chacha20poly1305` panic）。
- **一等入站（v0.8）：** 入站配置文件（AWG / VLESS+REALITY / MTProxy）一次创建，部署到任意节点集，附带节点专属凭据 — 修改配置文件仅重新部署受影响节点。
- **带负载均衡策略的链层（v0.8）：** 链是由节点组层组成的有序列表 — `入口 → [Hop-1, Hop-2] → [出口-1, 出口-2, 出口-3]` — 附带每层策略：Round-robin（fallback，默认 — 修补的单连接轮询）、urltest、failover、selector。
- **简化客户端（v0.8）：** 添加客户端 = 选择名称和可用的链；AWG peer 和 VLESS UUID 自动衍生。包含订阅 URL、链配置和 QR 码。
- **故障转移与负载均衡：** `urltest`、`failover`、`selector` 以及修补后的 per-connection round-robin `fallback`。
- **带回滚的可靠部署：** 每次应用都执行 backup（cp，保留）→ cert → upload → `sing-box check`（显示 stderr）→ restart → 真实健康探测 → 失败时回滚；per-node lock 防止并发部署竞争。
- **备份与节点快速迁移：** 将整个面板（或单节点的便携身份）导出为 JSON 备份并恢复/迁移；当节点 IP 被封锁时，**Relocate** 将其移动到新 VPS — 保留节点的传输密钥，因此其他节点和现有客户端无需重新配置 — 并重新部署包含它的链。**Clone** 节点可创建具有新身份的副本。
- **加密异地备份：** 定时或按需通过 SSH 将带密码保护的面板加密副本推送到远程主机（scrypt KDF + AES-256-GCM）。
- **节点健康状态机：** 跟踪节点状态 `healthy → suspect → down → unreachable`（带迟滞特性），以及操作员标记的 **blocked** 状态。
- **用户向导与服务模型：** 通过引导向导添加用户，查看合成的 **Service**（用户在所有链上的合并视图），并获取分发给客户端的 **订阅 URL**。
- **现代 Web UI：** 蛛网拓扑编辑器（图边、持久化节点位置、原生 SVG 平移/缩放）、部署状态、审计日志、配置文件/服务、统一客户端、路由规则 — 基于 HTMX + TailwindCSS + DaisyUI + templ 构建。
- **后台自动应用：** 用户/入站变更触发后台 SSH 部署；per-host lock 序列化。
- **自动迁移（热备池）：** 当健康监测将节点转为 down/unreachable 时，编排器可自动将其迁移到 **备用 VPS**。
- **零停机用户管理：** 仅更改 peer 集合（添加/删除用户）的部署通过 **`awg set` 实时应用** — 无需重启 `awg-quick`，不断连。
- **AWG 诊断：** 节点行点击 **Diagnose**，通过 SSH 深度探测数据平面（接口状态、握手新鲜度、ip_forward、rp_filter、FORWARD 规则、sing-box 健康状况）。
- **单用户流量统计：** 内核 per-peer 计数器（`awg show transfer`）折叠为用户累计字节数。
- **自愈 NAT：** 当 fail2ban 或 Docker 清空 iptables 时，健康循环自动重新插入 FORWARD/MASQUERADE 规则。
- **路由器软件包（Keenetic + OpenWrt）：** 现成 `.ipk` 包，适用于 Keenetic Entware（mipsel/mips/aarch64）和 OpenWrt（procd） — 瘦身 + UPX 压缩（~3 MB）。
- **100% 独立：** Angry-BOX 附带自己的 **amnezia-box** 二进制文件（deps/），弱 VPS 无需编译 Go — 直接下载。
- **零占用：** 节点服务器仅运行裸 `sing-box` 核心；编排器完全位于你的控制机上。

## 截图

<div align="center">
  <img src="docs/assets/dashboard.png" alt="Dashboard" width="800" style="border-radius: 8px; box-shadow: 0 4px 10px rgba(0,0,0,0.5); margin-bottom: 20px;"/>
  <br>
  <em>Angry-BOX Web UI 仪表盘</em>
  <br><br>
  <img src="docs/assets/spider.png" alt="蛛网拓扑编辑器" width="800" style="border-radius: 8px; box-shadow: 0 4px 10px rgba(0,0,0,0.5); margin-bottom: 20px;"/>
  <br>
  <em>蛛网拓扑编辑器 — 多跳链拓扑图</em>
  <br><br>
  <img src="docs/assets/users.png" alt="用户" width="800" style="border-radius: 8px; box-shadow: 0 4px 10px rgba(0,0,0,0.5); margin-bottom: 20px;"/>
  <br>
  <em>用户 — 单用户协议、链访问与生命周期状态</em>
</div>

> 截图反映当前版本功能（节点健康状态机、用户向导、克隆/迁移/自动迁移、加密异地备份、AWG 诊断、蛛网图编辑器、部署状态、接管、审计）。

## 架构

与需要在每台服务器上安装重型代理的传统面板不同，Angry-BOX 采用**无状态无代理方法**：

```mermaid
graph LR
    Client((客户端<br/>AmneziaWG)) -->|混淆流量| Node1[入口节点<br/>VPS 1]
    Node1 -->|XHTTP / Reality| Node2[出口节点<br/>VPS 2]
    Node2 -->|干净流量| Web((互联网))

    Orchestrator[Angry-BOX<br/>控制服务器] -.->|SSH / 配置推送| Node1
    Orchestrator -.->|SSH / 配置推送| Node2
```

## 入门

### 1. 安装

从 [Releases](https://github.com/AlexeyLCP/angry-box/releases) 页面下载最新版本，或运行安装脚本：

```bash
curl -fsSL https://raw.githubusercontent.com/AlexeyLCP/angry-box/main/scripts/install.sh | sh
```

### 2. 启动 Web UI

```bash
angry-box serve -listen 0.0.0.0:8090
```

*注意：首次运行会为 Web UI 生成随机安全密码。*

### 3. CLI 快速开始

```bash
# 1. 添加你的 VPS 节点
angry-box host add entry-node --addr 1.2.3.4:22 --user root --key ~/.ssh/id_ed25519
angry-box host add exit-node --addr 5.6.7.8:22 --user root --key ~/.ssh/id_ed25519

# 2. 将 amnezia-box sing-box 二进制部署到节点
#    (-sudo 用于具有免密 sudo 的非 root SSH 用户；-install-awg 还会安装 AmneziaWG 内核模块)
angry-box deploy -addr 1.2.3.4 -key ~/.ssh/id_ed25519 -sudo
angry-box deploy -addr 5.6.7.8 -key ~/.ssh/id_ed25519 -sudo

# 3. 创建链
angry-box chain create my-chain --nodes entry-node,exit-node --user-protocol awg --transport xhttp

# 4. 应用链（生成 + 推送配置到所有节点，失败时回滚）
angry-box apply-chain my-chain

# 5. 在本地生成独立配置（例如 REALITY+XHTTP）而不推送
angry-box config -port 443

# 6. 备份面板 + 将被封锁节点迁移到新 VPS
angry-box backup store -o panel-backup.json          # 整个面板备份
angry-box backup node entry-node -o entry-node.json  # 单节点便携身份
angry-box restore panel-backup.json                 # 自动识别 store vs node 并恢复
# 当 entry-node 的 IP 被封时，将其移动到新 VPS — 传输密钥保留，
# 其他节点和现有客户端无需重新配置；包含该节点的所有链均自动重新部署：
angry-box relocate entry-node --addr 9.9.9.9:22
```

### 4. 在路由器上（Keenetic / OpenWrt）

```bash
# Keenetic (Entware) — 从 Releases 选择对应型号的软件包：
opkg install angry-box_v0.7.0_mipsel-3.4-kn.ipk      # MT7621 等
# OpenWrt:
opkg install angry-box_v0.7.0_aarch64_cortex-a53.ipk
# 面板监听在 127.0.0.1:9080 (loopback) — 通过 SSH 隧道访问。
```

**接管**（检测 + 转换现有 VPN 服务器）可从 Web UI 使用：打开节点 → **接管**按钮。它检测 AWG/sing-box/Xray/MTProxy，将配置转换为相同设置的 sing-box，禁用旧 VPN，如果 sing-box 失败则自动回滚。**备份与节点迁移**也可在 Web UI 中使用：设置 → 备份（导出/导入面板），节点行 → **导出**（下载节点身份）+ **Relocate**（迁移节点到新 VPS）。

## 第三方组件

- **[sing-box](https://github.com/SagerNet/sing-box)** 和 **[amnezia-box](https://github.com/AlexeyLCP/amnezia-box)**（我们 fork 的 sing-box 1.14，GPLv3）
- **[AmneziaWG Linux Kernel Module](https://github.com/amnezia-vpn/amneziawg-linux-kernel-module)** (GPLv2)
- **[awg-multi-script by pumbaX](https://github.com/pumbaX/awg-multi-script)** (MIT) — AmneziaWG 混淆最佳实践（Jc/Jmin/Jmax/S1-S4/H1-H4 不变量、CPS 包生成）
- **[awg-manager by hoaxisr](https://github.com/hoaxisr/awg-manager)** (MIT) — 实时 QUIC 签名捕获算法（"接管现有 VPN" 捕获逻辑：通过 UDP 连接到 domain:443，发送 QUIC Initial，捕获服务器响应包作为 I1-I5）
- **[templ](https://github.com/a-h/templ)** (MIT) — Web UI 的 HTML 模板
- **[golang.org/x/crypto/ssh](https://go.googlesource.com/crypto)** (BSD-3-Clause) — Go SSH 客户端
- **HTMX、TailwindCSS 和 DaisyUI** (MIT / BSD)

## 致谢

- 特别感谢 **Aleksandr SacredX** 的广泛测试和宝贵建议。
- 实时 QUIC 签名捕获（Angry-BOX 用它为真实域名的 QUIC 轮廓提取指纹，用于 AmneziaWG CPS I1-I5）移植自 **[hoaxisr/awg-manager](https://github.com/hoaxisr/awg-manager)**。
- AmneziaWG 混淆参数生成（配置文件 + 不变量）和合成的 CPS 包生成器（用于 I1-I5 的 TLS/DNS/SIP/QUIC ClientHello 形状）移植自 **[pumbaX/awg-multi-script](https://github.com/pumbaX/awg-multi-script)**。
- XHTTP 传输 + 高级混淆字段源自 **Xray 团队 (RPRX)**；逼真的 HTTP 头生成受 **[NaiveProxy](https://github.com/SagerNet/naive)** 启发；分块碎片化思维采纳自 **Hysteria2 Gecko** 设计。
- **Hysteria2**、**NaiveProxy**、**Telemt**，以及众多俄罗斯、伊朗和中国抗审查研究员。

## 从源码构建

```bash
git clone https://github.com/AlexeyLCP/angry-box.git
cd angry-box

# 生产构建（全部嵌入）
go build -o angry-box ./cmd/angry-box

# 开发模式（从磁盘加载静态文件，无需重新构建即可编辑）
ANGRY_BOX_DEV=1 go run ./cmd/angry-box serve
```

## ☕ 支持项目

Angry-BOX 免费用于个人及非商业用途。如果编排器为您节省了时间，欢迎支持开发：

| 方式 | 详情 |
|---|---|
| 🇷🇺 **YooMoney**（卢布，俄罗斯） | [yoomoney.ru/to/41001989176429](https://yoomoney.ru/to/41001989176429) |
| 💎 **USDT (TON)** | `UQC48dE4i35bjEU4jljx0h1CGeXMu77eKZwN5W4gbcibmqDs` |
| 💠 **USDT (ERC-20)** | `0xA49aBc042c5BB3d682788D3DEB2eAC833343a873` |

捐赠是对作者的感谢，而非购买：捐赠不授予商业许可，也不改变下方的许可证条款。

## 许可证

**PolyForm Noncommercial License 1.0.0**

免费用于个人、教育和研究目的。商业使用需要书面许可。

完整文本见 [LICENSE](LICENSE)。