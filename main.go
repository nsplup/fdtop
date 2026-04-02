package main

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// --- 数据处理 ---

type ProcStats struct {
	PID, Name                      string
	Total, File, Socket, Pipe, Anon int
}

// 包含系统级/用户级统计的载体
type statsPayload struct {
	procStats []ProcStats
	currentFD int
	maxFD     int
}

type tickMsg statsPayload

func cleanProcessName(rawCmd []byte) string {
	if len(rawCmd) == 0 {
		return ""
	}
	parts := strings.Split(string(rawCmd), "\x00")
	firstArg := parts[0]
	if strings.Contains(firstArg, " ") {
		firstArg = strings.Fields(firstArg)[0]
	}
	return filepath.Base(firstArg)
}

// 获取 FD 限制和当前使用量
func getFdLimits() (current int, max int) {
	// 获取当前进程可用的资源限制 (Soft Limit)
	var rLimit syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &rLimit); err == nil {
		max = int(rLimit.Cur)
	}

	// 计算当前所有进程打开的句柄总数（本程序看到的范围）
	// 注意：普通用户只能看到自己进程的 fd 详情
	entries, _ := os.ReadDir("/proc")
	for _, entry := range entries {
		if !entry.IsDir() || !isNumeric(entry.Name()) {
			continue
		}
		fds, err := os.ReadDir(filepath.Join("/proc", entry.Name(), "fd"))
		if err == nil {
			current += len(fds)
		}
	}
	return
}

func getProcStats() statsPayload {
	entries, _ := os.ReadDir("/proc")
	var stats []ProcStats
	for _, entry := range entries {
		pid := entry.Name()
		if !entry.IsDir() || !isNumeric(pid) {
			continue
		}
		var name string
		if cmdline, err := os.ReadFile(filepath.Join("/proc", pid, "cmdline")); err == nil && len(cmdline) > 0 {
			name = cleanProcessName(cmdline)
		}
		if name == "" {
			comm, _ := os.ReadFile(filepath.Join("/proc", pid, "comm"))
			name = strings.TrimSpace(string(comm))
		}
		fdDir := filepath.Join("/proc", pid, "fd")
		fds, err := os.ReadDir(fdDir)
		if err != nil {
			// 如果由于权限无法读取该进程 fd，则跳过或标记
			continue
		}
		ps := ProcStats{PID: pid, Name: name, Total: len(fds)}
		for _, fd := range fds {
			link, _ := os.Readlink(filepath.Join(fdDir, fd.Name()))
			switch {
			case strings.HasPrefix(link, "/"):
				ps.File++
			case strings.HasPrefix(link, "socket:"):
				ps.Socket++
			case strings.HasPrefix(link, "pipe:"):
				ps.Pipe++
			case strings.HasPrefix(link, "anon_inode:"):
				ps.Anon++
			}
		}
		stats = append(stats, ps)
	}
	sort.Slice(stats, func(i, j int) bool { return stats[i].Total > stats[j].Total })

	curr, max := getFdLimits()
	return statsPayload{procStats: stats, currentFD: curr, maxFD: max}
}

func isNumeric(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// --- 样式逻辑 ---

var (
	headerStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("229")).
			Background(lipgloss.Color("62")).
			Padding(0, 1)

	rowStyle = lipgloss.NewStyle().Padding(0, 1)

	userTagStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("86")).
			Underline(true)

	infoStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("241"))
)

// --- TUI 模型 ---

type model struct {
	viewport      viewport.Model
	ready         bool
	data          []ProcStats
	currentFD     int
	maxFD         int
	userName      string
	processWidth  int
	windowWidth   int
}

const (
	pidColW    = 10
	totalColW  = 10
	fileColW   = 10
	socketColW = 12
	pipeColW   = 10
	anonColW   = 10
	sideMargin = 4
)

func (m model) Init() tea.Cmd {
	return tea.Batch(
		func() tea.Msg { return tickMsg(getProcStats()) },
		tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(getProcStats()) }),
	)
}

func (m model) renderRow(vals []string, isHeader bool) string {
	widths := []int{pidColW, m.processWidth, totalColW, fileColW, socketColW, pipeColW, anonColW}
	var renderedCols []string

	for i, val := range vals {
		style := rowStyle.Copy().Width(widths[i])
		if isHeader {
			style = headerStyle.Copy().Width(widths[i])
		}

		text := val
		if len(text) > widths[i] {
			text = text[:widths[i]-3] + "..."
		}
		renderedCols = append(renderedCols, style.Render(text))
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, renderedCols...)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.String() == "q" || msg.String() == "ctrl+c" {
			return m, tea.Quit
		}

	case tea.WindowSizeMsg:
		m.windowWidth = msg.Width
		fixedWidths := pidColW + totalColW + fileColW + socketColW + pipeColW + anonColW + sideMargin
		m.processWidth = msg.Width - fixedWidths
		if m.processWidth < 15 {
			m.processWidth = 15
		}

		if !m.ready {
			m.viewport = viewport.New(msg.Width, msg.Height-8) // 留出更多空间给标题
			m.ready = true
		} else {
			m.viewport.Width, m.viewport.Height = msg.Width, msg.Height-8
		}
		m.updateViewportContent()

	case tickMsg:
		m.data = msg.procStats
		m.currentFD = msg.currentFD
		m.maxFD = msg.maxFD
		m.updateViewportContent()
		return m, tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(getProcStats()) })
	}

	m.viewport, cmd = m.viewport.Update(msg)
	return m, cmd
}

func (m *model) updateViewportContent() {
	var lines []string
	for _, s := range m.data {
		lines = append(lines, m.renderRow([]string{
			s.PID, s.Name, fmt.Sprint(s.Total),
			fmt.Sprint(s.File), fmt.Sprint(s.Socket),
			fmt.Sprint(s.Pipe), fmt.Sprint(s.Anon),
		}, false))
	}
	currOffset := m.viewport.YOffset
	m.viewport.SetContent(strings.Join(lines, "\n"))
	m.viewport.YOffset = currOffset
}

func (m model) View() string {
	if !m.ready {
		return "\n  Initializing..."
	}

	// 顶部状态栏
	userDisplay := userTagStyle.Render(strings.ToUpper(m.userName))
	fdInfo := infoStyle.Render(fmt.Sprintf("Usage: %d / %d", m.currentFD, m.maxFD))
	
	header := m.renderRow([]string{"PID", "PROCESS", "TOTAL", "FILE", "SOCKET", "PIPE", "ANON"}, true)

	content := m.viewport.View()
	var indentedContent strings.Builder
	for _, line := range strings.Split(content, "\n") {
		indentedContent.WriteString("  " + line + "\n")
	}

	return fmt.Sprintf(
		"\n  USER: %s    %s\n\n  %s\n%s\n  Press q to exit.",
		userDisplay,
		fdInfo,
		header,
		indentedContent.String(),
	)
}

func main() {
	// 获取当前用户名
	currentUser, err := user.Current()
	username := "unknown"
	if err == nil {
		username = currentUser.Username
	}

	m := model{userName: username}
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
	if _, err := p.Run(); err != nil {
		fmt.Printf("Error: %v", err)
		os.Exit(1)
	}
}