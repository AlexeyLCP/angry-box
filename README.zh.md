**Languages:** [English](README.md) | [Русский](README.ru.md) | [中文](README.zh.md) | [فارسی](README.fa.md)

# Angry-BOX

**轻量级 SSH-only 编排器**，用于 **sing-box**（主要）和 **xray**（次要）。

无需在节点上部署持久化代理。所有管理均通过 SSH 完成。可在远程服务器和路由器（包括 Keenetic）上部署最小代理配置。

## 主要特性

- 纯 SSH 控制平面，无需在目标上保留代理
- 2026 年强力隐身预设（俄罗斯/伊朗/中国/最大隐身）
- 高级 AWG + 完整的 CPS + 真实 QUIC/SIP/DNS 生成器
- 高质量 XHTTP 传输（填充、XMUX、真实请求头），同时支持 sing-box 和 xray
- 稳定的用户凭证（AWG 密钥和 CPS 每个链路只生成一次）
- 优秀的路由器支持（Keenetic .ipk + OpenWRT）
- 原生 Windows 版本
- Web UI + 完整 CLI

## 快速开始

```bash
# 1. 安装
curl -fsSL https://raw.githubusercontent.com/alexeylcp/angry-box/main/scripts/install.sh | sh

# 2. 添加节点
angry-box host add node1 --addr 203.0.113.10:22 --user root --key ~/.ssh/id_ed25519

# 3. 使用 2026 强力预设创建链路
angry-box chain create mychain --nodes node1 --strategy urltest --profile pro_2026 --transport xhttp --user-protocol awg

# 4. 部署
angry-box apply-chain mychain
```

Web UI 默认地址：`http://localhost:8090`

## 安装

### 一键安装脚本（推荐，Linux / Keenetic）

```bash
# 最新版
curl -fsSL https://raw.githubusercontent.com/alexeylcp/angry-box/main/scripts/install.sh | sh

# 指定版本
curl -fsSL https://raw.githubusercontent.com/alexeylcp/angry-box/main/scripts/install.sh | sh -s -- --version 0.2.1
```

### 预编译二进制

从 [Releases](https://github.com/alexeylcp/angry-box/releases) 下载。

**Linux**
```bash
tar -xzf angry-box-0.2.1-linux-amd64.tar.gz
cd angry-box-0.2.1-linux-amd64
./angry-box --help
```

**Windows**
- 下载 `angry-box-0.2.1-windows-amd64.zip` 或 `.exe`
- 解压后直接运行 `angry-box.exe`
- Web UI: `http://localhost:8090`

### 路由器（Keenetic / OpenWRT）

详见下方路由器安装部分。

## 架构

Angry-BOX 仅作为**控制平面**存在。

- 编排器本身不转发流量
- 所有操作通过 SSH 完成
- 远程节点上仅部署轻量代理（sing-box 或 xray）+ 小型配置

**两种连接类型：**
- **Transport**：用于链路内部跳板（推荐 XHTTP）
- **User**：真实客户端入口（TUIC v5 或 AmneziaWG）

## 2026 隐身预设

项目内置针对当前 DPI 优化的专业预设：

| 预设                      | 针对环境               | 主要技术                           |
|---------------------------|------------------------|------------------------------------|
| `russia_2026`             | 俄罗斯                 | 平衡型 XHTTP + AWG                 |
| `iran_2026`               | 伊朗                   | 激进 XHTTP + Reality               |
| `china_2026`              | 中国                   | 强力混淆 + 分片                    |
| `maximum_stealth_2026`    | 最高隐蔽性             | 完整 XHTTP + AWG CPS               |
| `pro_2026`                | 专业使用               | 强制 CPS 3 级 + 1200B QUIC         |
| `xhttp_max_stealth_2026`  | 极限 XHTTP             | 最大填充 + XMUX                    |

## 路由器支持

Angry-BOX 提供原生 `.ipk` 包。

推荐直接从 Releases 下载对应架构的包安装。

## 从源码构建

```bash
git clone https://github.com/alexeylcp/angry-box.git
cd angry-box
CGO_ENABLED=0 go build -o angry-box ./cmd/angry-box
make package-all
```

## 致谢

本项目大量借鉴了反审查社区的公开研究成果。

核心贡献者包括：
- pumbaX / awg-multi-script
- Xray 团队（RPRX）
- Hysteria2、NaiveProxy、Telemt 等社区项目

## 许可证

PolyForm Noncommercial License 1.0.0

## 支持

- 问题反馈 → [GitHub Issues](https://github.com/alexeylcp/angry-box/issues)
- 讨论 → GitHub Discussions

---

**当前版本：** 0.2.1 — 改进路由器打包、Windows 支持及文档。