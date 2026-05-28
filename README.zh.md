# Angry-BOX

**语言:** [English](README.md) | [Русский](README.ru.md) | [中文](README.zh.md) | [فارسی](README.fa.md)

轻量级编排器，通过 **仅 SSH** 方式在远程机器上管理高混淆代理链。

**sing-box** 为主后端，**xray** 为次要后端（尽力而为）。

## 架构原则

- 编排器仅作为“控制大脑”，绝不参与实际流量转发。
- 完全通过 **SSH** 管理，节点上不部署常驻代理。
- 远程机器（VPS、Keenetic 等路由器）仅部署实际代理（sing-box 或 xray）+ 最小配置 + 启动脚本。
- 可以在 Keenetic 上运行 Angry-BOX 本身，此时它仅作为管理头，**不成为**链路中的代理节点。

### 两种连接类型

- **传输连接（Transport）**：用于链路内部跳点连接（2026 年推荐 XHTTP）。
- **用户连接（User）**：面向最终客户端的入口（TUIC v5、带高级 CPS 的 AmneziaWG 等）。

## 2026 年混淆预设（安全优先）

可通过配置文件或 `--profile` 指定全局/链路级预设。

当前预设：
- `russia_2026`、`iran_2026`、`china_2026` — 区域平衡型
- `maximum_stealth_2026` — 激进型
- `pro_2026` — 完整 pumbaX Pro 2026 参数 + 完整 AWG CPS I1-I5 链（级别 3，主打 QUIC）
- `xhttp_max_stealth_2026` — **极端** XHTTP 专用预设（重度随机填充、激进 XMUX、上下行分离提示 + pro 级 AWG）

**Security > Compatibility** 是 `pro_2026` 和 `xhttp_max_stealth_2026` 的明确策略。它们提供 2026 年已知最强的 DPI 抵抗能力（针对 RKN、GFW、伊朗系统），代价是客户端配置更大，可能与极旧客户端不兼容。

AWG 入口凭证（服务器密钥对 + CPS I1-I5）在创建链路时**一次性生成**，后续 apply 不会轮换。

### 高级 XHTTP 混淆

我们实现了大量 2025–2026 年社区最前沿的技术：
- 随机范围的 Header Padding
- XMUX 风格的多路复用控制
- 真实浏览器风格的请求头（受真实 Chromium 启发）
- 上下行分离提示
- 模式选择（packet-up / stream-up）

同时支持 sing-box 和 xray 后端。

## 安装

推荐使用官方安装脚本：

```bash
# 最新版
curl -fsSL https://raw.githubusercontent.com/AlexeyLCP/lucx/main/scripts/install.sh | sh

# 指定版本
curl -fsSL https://raw.githubusercontent.com/AlexeyLCP/lucx/main/scripts/install.sh | sh -s -- --version 0.2.0

# 本地二进制
sh scripts/install.sh --local ./angry-box
```

脚本会自动识别 Linux（systemd）和 Keenetic（Entware）环境。

### 卸载与更新

```bash
sh scripts/install.sh --uninstall
sh scripts/install.sh --version 0.3.0
```

## 快速开始

```bash
# 1. 添加主机
angry-box host add node1 --addr 203.0.113.10:22 --user root --key ~/.ssh/id_ed25519

# 2. 部署 sing-box
angry-box deploy --host node1

# 3. 使用强 2026 预设创建链路
angry-box chain create mychain --nodes node1 --strategy urltest --profile pro_2026 --transport xhttp --user-protocol awg

# 4. 应用（将获得包含 AWG 密钥 + CPS 的丰富报告）
angry-box apply-chain mychain

# 5. 查看状态
angry-box chain show mychain
```

独立生成配置（无需链路）：

```bash
angry-box config -type user --protocol awg --profile xhttp_max_stealth_2026
```

## 功能特性

- 纯 SSH 管理 + 失败自动回滚
- 详细的 ApplyReport（包含 AWG 服务端公钥和稳定的 CPS I1-I5）
- AWG 入口凭证稳定（创建时生成一次）
- 基于社区研究的先进 XHTTP 混淆参数
- 模块化 2026 预设 + 支持外部 JSON
- apply-chain 与独立 `config` 命令完全对等

## 支持

- Bug 反馈和功能请求请通过 GitHub Issues。
- 一般讨论和被墙环境下的配置帮助请使用 GitHub Discussions。
- 来自俄罗斯、伊朗、中国的真实测试反馈对改进预设非常有价值。

## 语言

[English](README.md) | [Русский](README.ru.md) | [中文](README.zh.md) | [فارسی](README.fa.md)

## 致谢 / Credits

详细致谢见英文版 README 底部。项目大量借鉴了反审查社区的公开研究与工具。

## 许可证

PolyForm Noncommercial License 1.0.0