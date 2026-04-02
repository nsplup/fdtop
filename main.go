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
	ETime                          string
}

type statsPayload struct {
	procStats []ProcStats
	currentFD int
	maxFD     int
}

type tickMsg statsPayload

var btime int64

func init() {
	data, _ := os.ReadFile("/proc/stat")
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "btime") {
			fmt.Sscanf(line, "btime %d", &btime)
			break
		}
	}
}

func getETime(pid string) string {
	statPath := filepath.Join("/proc", pid, "stat")
	data, err := os.ReadFile(statPath)
	if err != nil {
		return "00:00"
	}
	fields := strings.Fields(string(data))
	if len(fields) < 22 {
		return "00:00"
	}

	var startTicks int64
	fmt.Sscanf(fields[21], "%d", &startTicks)

	clkTck := int64(100)
	startTimeSec := btime + (startTicks / clkTck)
	duration := time.Since(time.Unix(startTimeSec, 0))

	days := int(duration.Hours()) / 24
	hours := int(duration.Hours()) % 24
	mins := int(duration.Minutes()) % 60
	secs := int(duration.Seconds()) % 60

	if days > 0 {
		return fmt.Sprintf("%dd-%02d:%02d:%02d", days, hours, mins, secs)
	}
	if hours > 0 {
		return fmt.Sprintf("%02d:%02d:%02d", hours, mins, secs)
	}
	return fmt.Sprintf("%02d:%02d", mins, secs)
}

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

func getFdLimits() (current int, max int) {
	var rLimit syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &rLimit); err == nil {
		max = int(rLimit.Cur)
	}
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
			continue
		}
		ps := ProcStats{
			PID:   pid,
			Name:  name,
			Total: len(fds),
			ETime: getETime(pid),
		}
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

	selectedRowStyle = lipgloss.NewStyle().
				Padding(0, 1).
				Background(lipgloss.Color("62")).
				Foreground(lipgloss.Color("229"))

	userTagStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("86")).
			Underline(true)

	infoStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("241"))
)

// --- TUI 模型 ---

type model struct {
	viewport     viewport.Model
	ready        bool
	data         []ProcStats
	currentFD    int
	maxFD        int
	userName     string
	processWidth int
	windowWidth  int
	windowHeight int
	marginTop    int
	selectedPIDs map[string]bool
}

const (
	pidColW    = 10
	totalColW  = 10
	fileColW   = 10
	socketColW = 10
	pipeColW   = 10
	anonColW   = 10
	etimeColW  = 15
	sideMargin = 6
)

func (m model) Init() tea.Cmd {
	return tea.Batch(
		func() tea.Msg { return tickMsg(getProcStats()) },
		tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(getProcStats()) }),
	)
}

// 计算当前内容区域的总宽度
func (m model) getContentWidth() int {
	return pidColW + m.processWidth + etimeColW + totalColW + fileColW + socketColW + pipeColW + anonColW
}

func (m model) renderRow(vals []string, isHeader bool, isSelected bool) string {
	widths := []int{pidColW, m.processWidth, etimeColW, totalColW, fileColW, socketColW, pipeColW, anonColW}
	var renderedCols []string

	for i, val := range vals {
		var style lipgloss.Style
		if isHeader {
			style = headerStyle.Copy().Width(widths[i])
		} else if isSelected {
			style = selectedRowStyle.Copy().Width(widths[i])
		} else {
			style = rowStyle.Copy().Width(widths[i])
		}

		text := val
		if len(text) > widths[i] {
			text = text[:widths[i]-3] + "..."
		}
		renderedCols = append(renderedCols, style.Render(text))
	}

	// 水平居中处理：在左侧补齐空格
	contentWidth := m.getContentWidth()
	marginLeft := (m.windowWidth - contentWidth) / 2
	if marginLeft < 0 {
		marginLeft = 0
	}
	return strings.Repeat(" ", marginLeft) + lipgloss.JoinHorizontal(lipgloss.Top, renderedCols...)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.String() == "q" || msg.String() == "ctrl+c" {
			return m, tea.Quit
		}

	case tea.MouseMsg:
		if msg.Type == tea.MouseLeft && msg.Action == tea.MouseActionPress {
			// 动态计算实际渲染时的 marginTop
			// uiHeight 包含: userRow(1) + 空行(1) + Header(1) + Viewport高度 + Footer(1)
			// 总附加高度准确值为 4 行
			vpLines := lipgloss.Height(m.viewport.View())
			uiHeight := vpLines + 4
			
			actualMarginTop := (m.windowHeight - uiHeight) / 2
			if actualMarginTop < 0 {
				actualMarginTop = 0
			}

			// 修正点击坐标：减去实际的 marginTop 以及前置的 3 行占用 (userRow, 空行, Header)
			targetLine := msg.Y - actualMarginTop - 3 + m.viewport.YOffset
			
			if targetLine >= 0 && targetLine < len(m.data) {
				pid := m.data[targetLine].PID
				m.selectedPIDs[pid] = !m.selectedPIDs[pid]
				m.updateViewportContent()
			}
		}

	case tea.WindowSizeMsg:
		m.windowWidth = msg.Width
		m.windowHeight = msg.Height
		
		fixedWidths := pidColW + etimeColW + totalColW + fileColW + socketColW + pipeColW + anonColW + sideMargin
		m.processWidth = msg.Width - fixedWidths
		if m.processWidth < 15 {
			m.processWidth = 15
		}

		// 限制 Viewport 高度，为上下留白和状态栏腾出空间
		// 减去的内容包括：用户状态栏(1) + 空行(1) + Header(1) + 退出提示(1) + 额外留白(4)
		vpHeight := msg.Height - 8
		if vpHeight < 5 { vpHeight = 5 }

		if !m.ready {
			m.viewport = viewport.New(msg.Width, vpHeight)
			m.ready = true
		} else {
			m.viewport.Width, m.viewport.Height = msg.Width, vpHeight
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
		isSelected := m.selectedPIDs[s.PID]
		lines = append(lines, m.renderRow([]string{
			s.PID, s.Name, s.ETime, fmt.Sprint(s.Total),
			fmt.Sprint(s.File), fmt.Sprint(s.Socket),
			fmt.Sprint(s.Pipe), fmt.Sprint(s.Anon),
		}, false, isSelected))
	}
	currOffset := m.viewport.YOffset
	m.viewport.SetContent(strings.Join(lines, "\n"))
	m.viewport.YOffset = currOffset
}

func (m model) View() string {
	if !m.ready {
		return "Initializing..."
	}

	// 1. 准备中间的主体 UI 块
	contentWidth := m.getContentWidth()
	marginLeft := (m.windowWidth - contentWidth) / 2
	if marginLeft < 0 { marginLeft = 0 }
	indent := strings.Repeat(" ", marginLeft)

	userRow := fmt.Sprintf("%sUSER: %s    %s", 
		indent,
		userTagStyle.Render(strings.ToUpper(m.userName)), 
		infoStyle.Render(fmt.Sprintf("Usage: %d / %d", m.currentFD, m.maxFD)))
	
	header := m.renderRow([]string{"PID", "PROCESS", "ETIME", "TOTAL", "FILE", "SOCKET", "PIPE", "ANON"}, true, false)
	
	footer := fmt.Sprintf("\n%s%s", indent, infoStyle.Render("Press q to exit • Scroll to view more • Click to highlight"))

	// 组装内容
	mainUI := fmt.Sprintf("%s\n\n%s\n%s%s", userRow, header, m.viewport.View(), footer)

	// 2. 计算垂直居中偏移 (改为局部变量 marginTop)
	uiHeight := lipgloss.Height(mainUI)
	marginTop := (m.windowHeight - uiHeight) / 2
	if marginTop < 0 { 
		marginTop = 0 
	}

	return strings.Repeat("\n", marginTop) + mainUI
}

func main() {
	currentUser, err := user.Current()
	username := "unknown"
	if err == nil {
		username = currentUser.Username
	}

	m := model{
		userName:     username,
		selectedPIDs: make(map[string]bool),
	}
	// 启用鼠标支持和全屏模式
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
	if _, err := p.Run(); err != nil {
		fmt.Printf("Error: %v", err)
		os.Exit(1)
	}
}