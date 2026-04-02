*本项目与 README 由 `Gemini` 完成，`我` 监制*

# FD Monitor (Go TUI)

一个轻量级的 Linux 进程文件句柄（File Descriptor）实时监控工具。它可以直观地显示系统中各进程打开的句柄数量，并按资源类型（文件、套接字、管道等）进行分类统计。

## 🚀 功能特性

* **实时监控**：每秒自动刷新一次系统进程状态。
* **分类统计**：详细展示每个进程的句柄分布情况：
    * **FILE**: 常规文件
    * **SOCKET**: 网络连接
    * **PIPE**: 进程间管道
    * **ANON**: 匿名句柄（如 epoll, eventfd 等）
* **系统级视图**：显示当前用户可感知的总 FD 使用量与系统软限制（Soft Limit）。
* **交互式界面**：支持鼠标滚动和窗口自适应缩放。

## 🛠️ 技术栈

* **语言**: [Go](https://go.dev/)
* **TUI 框架**: [Bubble Tea](https://github.com/charmbracelet/bubbletea)
* **样式/布局**: [Lip Gloss](https://github.com/charmbracelet/lipgloss)
* **视图组件**: [Bubbles (Viewport)](https://github.com/charmbracelet/bubbles)
* **系统接口**: 直接读取 `/proc` 文件系统（仅限 Linux）。

## 📋 运行环境

* **操作系统**: Linux (由于依赖 `/proc` 路径，不支持 Windows/macOS)。
* **权限说明**: 
    * 普通用户运行：只能看到属于该用户的进程 FD 详情。
    * Root 用户运行：可以查看系统内所有进程的 FD 详情。

## 📥 安装与运行

确保你已安装 Go 1.18+ 环境，然后在项目根目录下执行：

```bash
# 下载依赖
go mod tidy

# 直接运行
go run main.go

# 编译成二进制文件
go build -o fd-monitor
./fd-monitor
```

## ⌨️ 操作指南

| 按键 | 功能 |
| :--- | :--- |
| `q` / `Ctrl+C` | 退出程序 |
| `Up` / `Down` | 向上/下滚动列表 |
| `Mouse Wheel` | 使用鼠标滚轮滚动查看 |

---

## 🔍 界面指标说明

| 字段 | 说明 |
| :--- | :--- |
| **PID** | 进程 ID |
| **PROCESS** | 进程名称（基于 cmdline 或 comm） |
| **TOTAL** | 该进程打开的总句柄数 |
| **FILE** | 定位到磁盘路径的常规文件 |
| **SOCKET** | 网络套接字（TCP/UDP/Unix Domain） |
| **PIPE** | 用于进程间通信的管道 |
| **ANON** | `anon_inode` 类型的特殊句柄 |

---

## 💡 实现原理

1.  **数据采集**：通过遍历 `/proc/[pid]/fd` 目录下的符号链接，利用 `os.Readlink` 获取句柄指向的真实资源。
2.  **分类逻辑**：
    * 以 `/` 开头识别为文件。
    * 以 `socket:` 开头识别为网络连接。
    * 以 `pipe:` 开头识别为管道。
3.  **并发模型**：利用 `tea.Tick` 创建时间循环，每秒触发一次 `tickMsg` 重新扫描 `/proc` 并更新 UI 模型。
4.  **自适应布局**：通过监听 `tea.WindowSizeMsg` 动态计算表格各列的宽度，确保在窄屏或宽屏下都有良好的展示效果。

---

**注意**: 本工具仅用于开发调试参考，在高负载生产环境下频繁扫描 `/proc` 可能会产生轻微的 CPU 消耗。