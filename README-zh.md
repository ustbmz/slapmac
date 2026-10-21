<p align="center">  
  <img src="doc/logo.png" alt="slapmac logo" width="200">  
</p>

# slapmac

[English](./README.md) | **简体中文**

拍打你的 MacBook，它会回应你——拍得越狠，叫得越惨。

使用 Apple Silicon 芯片的加速度传感器，检测笔记本电脑的物理撞击，并按力度分级播放音频回应。单个二进制文件，无依赖。

本项目是 [taigrr/spank](https://github.com/taigrr/spank) 的分支：重新设计了回应系统，并附带一个 macOS 菜单栏应用。

## 特性

- **力度分级回应** — 无模式切换。每次拍打自动测量力度并播放对应音效：轻拍触发 weak，越用力依次升级到 medium、strong。
- **菜单栏应用**（`spank.app`）— 启动即自动运行 spank，一键启停，常驻菜单栏。
- **简单打包** — 一条命令打出可分发的 `.app` 或 `.dmg`。
- 播放音量完全跟随系统音量，无应用内音量控制。
- 750ms 响应冷却时间防止连击刷屏，可通过 `--cooldown` 调整。
- `--stdio` JSON 协议供 GUI/自动化集成，`--debug` 实时诊断传感器数据。

## 系统要求

- 基于 Apple Silicon 的 macOS（任何 M2 及以上型号的 M 系列芯片，或特定的 M1 Pro 型号，不包括其他 M1/A 系列芯片！）
- `sudo`（用于访问加速度传感器）
- Go 1.26+ 和 Swift 编译器（如果从源代码构建）

## 安装

### 方式一：DMG

自行构建 GUI 安装包：

```bash
./menubar/build-dmg-gui.sh 1.0.0
# -> dist/spankbar_1.0.0_mac_arm64.dmg
```

挂载 DMG，把 `spank.app` 拖入「应用程序」，打开即自动运行。首次启动会请求一次管理员权限，把二进制安装到 `/usr/local/bin` 并写入限定范围的 sudoers 免密条目；之后启动完全无感。

> **注意：** 应用为 ad-hoc 签名。若被 Gatekeeper 拦截，执行一次 `xattr -cr /Applications/spank.app` 即可。

### 方式二：仅命令行

```bash
go build -o spank .
sudo cp spank /usr/local/bin/spank
sudo spank
```

## 使用方法

```bash
sudo spank            # 按拍打力度自动分级回应
sudo spank --fast     # 更快的轮询和更短的冷却时间
sudo spank --debug    # 实时传感器数据（采样率、maxMag、事件数）

# 使用振幅阈值调整灵敏度（默认：0.02）
sudo spank --min-amplitude 0.05   # 忽略非常轻的触碰

# 设置冷却时间（毫秒，默认：750）
sudo spank --cooldown 600

# 设置播放速度倍数（默认：1.0）
sudo spank --speed 0.7   # 更慢更深沉
sudo spank --speed 1.5   # 更快
```

### 力度分级回应

无模式切换——根据每次拍打的力度自动选择回应。阈值基于 M2 实测数据校准（普通拍打的振幅在 0.02–0.06g，重拍可达 0.10g 以上）：

| 拍打振幅     | 回应         |
| ------------ | ------------ |
| < 0.03g      | `weak.mp3`   |
| 0.03g – 0.05g | `medium.mp3` |
| ≥ 0.05g      | `strong.mp3` |

想换成自己的音效，直接替换 `audio/pain/*.mp3` 后重新编译——文件名就是接口。

### 检测调优

`--fast` 提供更灵敏的配置：更快的轮询（4ms vs 10ms）、更短的冷却（350ms vs 750ms）、更大的采样批次。

`--debug` 每秒输出一行：采样率、最新/最大幅值、RMS/峰值、事件数和当前阈值。用它确认你的拍打落在哪个区间，然后调整 `main.go` 里的分级常量。

## 构建

```bash
# CLI 二进制
go build -o spank .

# 菜单栏应用（dist/spank.app）
./menubar/build-app.sh 1.0.0

# 可分发的 DMG
./menubar/build-dmg-gui.sh 1.0.0   # GUI 应用 DMG
./scripts/build-dmg.sh 1.0.0       # CLI DMG（二进制 + install.sh）
```

## 工作原理

1. 通过 IOKit HID 直接读取加速度传感器原始数据（Apple SPU 传感器）
2. 运行振动检测算法（STA/LTA、CUSUM、峰度、峰值/MAD）
3. 检测到明显撞击后，对振幅分级并播放对应的内嵌 MP3
4. 播放音量完全跟随系统音量，无应用内音量控制
5. 750ms 响应冷却时间防止连击刷屏，可通过 `--cooldown` 调整

## 致谢

- 传感器读取与振动检测移植自 [olvvier/apple-silicon-accelerometer](https://github.com/olvvier/apple-silicon-accelerometer)。
- 上游项目：[taigrr/spank](https://github.com/taigrr/spank)。

## 许可证

MIT
