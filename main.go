package main

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// --- 数据结构 ---

type ProcStats struct {
	PID, Name                       string
	Total, File, Socket, Pipe, Anon int
	ETime                           string
}

type FdDetail struct {
	FD   string
	Type string
	Path string
}

type statsPayload struct {
	procStats []ProcStats
	currentFD int
	maxFD     int
}

type tickMsg statsPayload

// --- 全局变量与样式 ---

var btime int64

var (
	headerStyle      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("229")).Background(lipgloss.Color("62")).Padding(0, 1)
	rowStyle         = lipgloss.NewStyle().Padding(0, 1)
	selectedRowStyle = lipgloss.NewStyle().Padding(0, 1).Background(lipgloss.Color("62")).Foreground(lipgloss.Color("229"))
	userTagStyle     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("86")).Underline(true)
	infoStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	modalStyle       = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("62")).Padding(1)

	// FD 类型着色
	fileColor   = lipgloss.NewStyle().Foreground(lipgloss.Color("39"))
	socketColor = lipgloss.NewStyle().Foreground(lipgloss.Color("211"))
	pipeColor   = lipgloss.NewStyle().Foreground(lipgloss.Color("208"))
	anonColor   = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))

	// 二级界面列宽定义
	fdColW   = 10
	typeColW = 12
)

// --- 数据处理逻辑 ---

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
	startTimeSec := btime + (startTicks / 100)
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

func getFdDetails(pid string) []FdDetail {
	fdDir := filepath.Join("/proc", pid, "fd")
	entries, err := os.ReadDir(fdDir)
	if err != nil {
		return nil
	}
	var details []FdDetail
	for _, entry := range entries {
		fd := entry.Name()
		link, _ := os.Readlink(filepath.Join(fdDir, fd))
		var fdType string
		switch {
		case strings.HasPrefix(link, "socket:"):
			fdType = "SOCK"
		case strings.HasPrefix(link, "pipe:"):
			fdType = "PIPE"
		case strings.HasPrefix(link, "anon_inode:"):
			fdType = "ANON"
		default:
			fdType = "FILE"
		}
		details = append(details, FdDetail{FD: fd, Type: fdType, Path: link})
	}
	sort.Slice(details, func(i, j int) bool {
		iv, _ := strconv.Atoi(details[i].FD)
		jv, _ := strconv.Atoi(details[j].FD)
		return iv < jv
	})
	return details
}

func getProcStats() statsPayload {
	entries, _ := os.ReadDir("/proc")
	var stats []ProcStats
	for _, entry := range entries {
		pid := entry.Name()
		if !entry.IsDir() || !isNumeric(pid) {
			continue
		}
		name := ""
		if cmdline, err := os.ReadFile(filepath.Join("/proc", pid, "cmdline")); err == nil && len(cmdline) > 0 {
			parts := strings.Split(string(cmdline), "\x00")
			name = filepath.Base(strings.Fields(parts[0])[0])
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
		ps := ProcStats{PID: pid, Name: name, Total: len(fds), ETime: getETime(pid)}
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
	var rLimit syscall.Rlimit
	max := 0
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &rLimit); err == nil {
		max = int(rLimit.Cur)
	}
	curr := 0
	for _, s := range stats {
		curr += s.Total
	}
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

// --- TUI 模型 ---

type model struct {
	viewport       viewport.Model
	detailViewport viewport.Model
	ready          bool
	showDetail     bool
	detailTitle    string
	data           []ProcStats
	currentFD      int
	maxFD          int
	userName       string
	processWidth   int
	windowWidth    int
	windowHeight   int
	selectedPIDs   map[string]bool
}

const (
	pidColW, totalColW, fileColW, socketColW, pipeColW, anonColW = 10, 10, 10, 10, 10, 10
	etimeColW                                                   = 15
	sideMargin                                                  = 6
)

func (m model) Init() tea.Cmd {
	return tea.Batch(
		func() tea.Msg { return tickMsg(getProcStats()) },
		tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(getProcStats()) }),
	)
}

func (m model) renderRow(vals []string, isHeader, isSelected bool) string {
	widths := []int{pidColW, m.processWidth, etimeColW, totalColW, fileColW, socketColW, pipeColW, anonColW}
	var renderedCols []string
	for i, val := range vals {
		style := rowStyle.Copy().Width(widths[i])
		if isHeader {
			style = headerStyle.Copy().Width(widths[i])
		} else if isSelected {
			style = selectedRowStyle.Copy().Width(widths[i])
		}
		text := val
		if len(text) > widths[i] {
			text = text[:widths[i]-3] + "..."
		}
		renderedCols = append(renderedCols, style.Render(text))
	}
	contentWidth := pidColW + m.processWidth + etimeColW + totalColW + fileColW + socketColW + pipeColW + anonColW
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
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "esc":
			if m.showDetail {
				m.showDetail = false
				return m, nil
			}
		}

	case tea.MouseMsg:
		if m.showDetail {
			if msg.Type == tea.MouseRight {
				m.showDetail = false
				return m, nil
			}
			m.detailViewport, cmd = m.detailViewport.Update(msg)
			return m, cmd
		}

		vpLines := m.viewport.Height
		actualMarginTop := (m.windowHeight - (vpLines + 4)) / 2
		targetLine := msg.Y - actualMarginTop - 3 + m.viewport.YOffset

		if msg.Type == tea.MouseLeft && msg.Action == tea.MouseActionPress {
			if targetLine >= 0 && targetLine < len(m.data) {
				pid := m.data[targetLine].PID
				m.selectedPIDs[pid] = !m.selectedPIDs[pid]
				m.updateViewportContent()
			}
		} else if msg.Type == tea.MouseRight {
			if targetLine >= 0 && targetLine < len(m.data) {
				proc := m.data[targetLine]
				details := getFdDetails(proc.PID)
				m.detailTitle = fmt.Sprintf("[PID %s] %s", proc.PID, proc.Name)

				// 计算容器内可用尺寸 (Window - 边框 - Padding)
				containerW := m.windowWidth - 8 - 2 
				containerH := m.windowHeight - 6 - 2 - 2 // 减去 Header(1) 和 SubHeader(1)

				var lines []string
				for _, d := range details {
					fdCell := lipgloss.NewStyle().Width(fdColW).Render(d.FD)
					var st lipgloss.Style
					switch d.Type {
					case "SOCK": st = socketColor
					case "PIPE": st = pipeColor
					case "ANON": st = anonColor
					default:     st = fileColor
					}
					typeCell := st.Width(typeColW).Render(d.Type)
					pathW := containerW - fdColW - typeColW
					if pathW < 10 { pathW = 10 }
					pathCell := lipgloss.NewStyle().Width(pathW).Render(d.Path)
					lines = append(lines, lipgloss.JoinHorizontal(lipgloss.Top, fdCell, typeCell, pathCell))
				}

				m.detailViewport = viewport.New(containerW, containerH)
				m.detailViewport.SetContent(strings.Join(lines, "\n"))
				m.showDetail = true
			}
		}

	case tea.WindowSizeMsg:
		m.windowWidth, m.windowHeight = msg.Width, msg.Height
		fixedWidths := pidColW + etimeColW + totalColW + fileColW + socketColW + pipeColW + anonColW + sideMargin
		m.processWidth = msg.Width - fixedWidths
		if m.processWidth < 15 {
			m.processWidth = 15
		}
		vpHeight := msg.Height - 8
		if vpHeight < 5 {
			vpHeight = 5
		}
		if !m.ready {
			m.viewport = viewport.New(msg.Width, vpHeight)
			m.ready = true
		} else {
			m.viewport.Width, m.viewport.Height = msg.Width, vpHeight
			if m.showDetail {
				// Resize detail viewport
				m.detailViewport.Width = m.windowWidth - 10
				m.detailViewport.Height = m.windowHeight - 10
			}
		}
		m.updateViewportContent()

	case tickMsg:
		m.data, m.currentFD, m.maxFD = msg.procStats, msg.currentFD, msg.maxFD
		m.updateViewportContent()
		return m, tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(getProcStats()) })
	}

	if !m.showDetail {
		m.viewport, cmd = m.viewport.Update(msg)
	}
	return m, cmd
}

func (m *model) updateViewportContent() {
	var lines []string
	for _, s := range m.data {
		lines = append(lines, m.renderRow([]string{
			s.PID, s.Name, s.ETime, fmt.Sprint(s.Total),
			fmt.Sprint(s.File), fmt.Sprint(s.Socket),
			fmt.Sprint(s.Pipe), fmt.Sprint(s.Anon),
		}, false, m.selectedPIDs[s.PID]))
	}
	currOffset := m.viewport.YOffset
	m.viewport.SetContent(strings.Join(lines, "\n"))
	m.viewport.YOffset = currOffset
}

func (m model) View() string {
	if !m.ready {
		return "Initializing..."
	}

	contentWidth := pidColW + m.processWidth + etimeColW + totalColW + fileColW + socketColW + pipeColW + anonColW
	marginLeft := (m.windowWidth - contentWidth) / 2
	if marginLeft < 0 {
		marginLeft = 0
	}
	indent := strings.Repeat(" ", marginLeft)

	userRow := fmt.Sprintf("%sUSER: %s    %s", indent, userTagStyle.Render(strings.ToUpper(m.userName)),
		infoStyle.Render(fmt.Sprintf("Usage: %d / %d", m.currentFD, m.maxFD)))
	header := m.renderRow([]string{"PID", "PROCESS", "ETIME", "TOTAL", "FILE", "SOCKET", "PIPE", "ANON"}, true, false)
	footer := fmt.Sprintf("\n%s%s", indent, infoStyle.Render("Press q to Exit • Right-Click to View Details • Left-Click to Highlight"))

	mainUI := fmt.Sprintf("%s\n\n%s\n%s%s", userRow, header, m.viewport.View(), footer)

	if m.showDetail {
		// 1. 容器宽度计算
		innerW := m.windowWidth - 10 // modalStyle 的 Padding(1) 和 Border(1)
		
		// 2. PID 标题行：宽度强制等于 innerW
		detailHeader := headerStyle.Copy().Width(innerW).Render(m.detailTitle)

		// 3. 列头行
		hFD := lipgloss.NewStyle().Width(fdColW).Foreground(lipgloss.Color("241")).Render("FD")
		hType := lipgloss.NewStyle().Width(typeColW).Foreground(lipgloss.Color("241")).Render("TYPE")
		hPath := lipgloss.NewStyle().Width(innerW - fdColW - typeColW).Foreground(lipgloss.Color("241")).Render("PATH")
		subHeader := lipgloss.JoinHorizontal(lipgloss.Top, hFD, hType, hPath)

		// 4. 组装模态框内容
		modalContent := lipgloss.JoinVertical(lipgloss.Left,
			detailHeader,
			subHeader,
			m.detailViewport.View(),
		)

		modal := modalStyle.
			Width(m.windowWidth - 8).
			Height(m.windowHeight - 6).
			Render(modalContent)

		return lipgloss.Place(m.windowWidth, m.windowHeight, lipgloss.Center, lipgloss.Center, modal)
	}

	marginTop := (m.windowHeight - lipgloss.Height(mainUI)) / 2
	if marginTop < 0 {
		marginTop = 0
	}
	return strings.Repeat("\n", marginTop) + mainUI
}

func main() {
	currentUser, _ := user.Current()
	m := model{
		userName:     currentUser.Username,
		selectedPIDs: make(map[string]bool),
	}
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
	if _, err := p.Run(); err != nil {
		fmt.Printf("Error: %v", err)
		os.Exit(1)
	}
}