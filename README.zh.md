**Languages:** [English](README.md) | [Русский](README.ru.md) | [中文](README.zh.md) | [فارسی](README.fa.md)

# Angry-BOX

轻量级 SSH-only 编排器，主后端 **sing-box**，次要 **xray**。节点上无持久化 agent。

## 2026 隐身预设（核心特性）

我们吸收了 2026 年中社区最佳实践（pumbaX/awg-multi-script、Xray XHTTP #4113、Hysteria Gecko、NaiveProxy 等），并强制启用 “Security > Compatibility” 策略。

内置预设：
- russia_2026 / iran_2026 / china_2026 / maximum_stealth_2026
- pro_2026（强制 CPS level 3 + 1200B QUIC Initial）
- xhttp_max_stealth_2026（极致 XHTTP + CPS3 + QUIC）

AWG 支持完整的 I1-I5 生成器（QUIC 1200 字节、真实 SIP/DNS 等）。

**致谢**：pumbaX、RPRX/Xray 社区、Hysteria、NaiveProxy、Hiddify、3x-ui 等。

## 安装（含路由器 .ipk）

```bash
# Keenetic (mipsel)
opkg install angry-box_0.2.0_mipsel_24kc.ipk

# OpenWRT aarch64
opkg install angry-box_0.2.0_aarch64_cortex-a53.ipk
```

完整英文文档见 [README.md](README.md)。
