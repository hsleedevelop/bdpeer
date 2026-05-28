package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/hsleedevelop/bdpeer/internal/config"
	"github.com/hsleedevelop/bdpeer/internal/core"
	"github.com/hsleedevelop/bdpeer/internal/ui"
	"github.com/hsleedevelop/bdpeer/internal/update"
)

var debugLogger *log.Logger

// Set at build time via ldflags.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	args := os.Args[1:]
	debug := false
	filtered := args[:0]
	for _, a := range args {
		if a == "--debug" || a == "-d" {
			debug = true
			continue
		}
		filtered = append(filtered, a)
	}
	if len(filtered) > 0 {
		switch filtered[0] {
		case "--help", "-h", "help":
			fmt.Printf(`bdpeer %s — 크로스 플랫폼 P2P TUI 채팅

사용법:
  bdpeer              TUI 실행
  bdpeer --debug      현재 디렉토리에 bdpeer-debug.log 기록
  bdpeer --version    버전 확인
  bdpeer --update     최신 버전으로 자동 업데이트
  bdpeer --help       이 도움말 출력

TUI 키 바인딩:
  ↑ / ↓              피어 선택
  Enter               메시지 전송
  Tab                 채팅 ↔ 로그 패널 전환
  Ctrl+C              종료

TUI 커맨드 (입력창):
  /connect <multiaddr>   주소로 피어 직접 연결
  /file <경로>           파일 전송

`, version)
			return
		case "--version", "-v", "version":
			fmt.Printf("bdpeer %s (%s) built %s\n", version, commit, date)
			return
		case "--update", "update":
			runUpdate()
			return
		}
	}

	if debug {
		if err := initDebugLog(); err != nil {
			fmt.Fprintln(os.Stderr, "debug log:", err)
			os.Exit(1)
		}
		defer closeDebugLog()
		debugLogger.Printf("=== bdpeer %s (%s) started ===", version, commit)
	}

	cfgPath := config.DefaultPath()
	cfg, err := config.Load(cfgPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "config:", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	svc := core.NewService(cfg, cfgPath)
	sendCh := make(chan core.SendRequest, 16)
	nickCh := make(chan string, 1)
	connectCh := make(chan string, 4)
	if cfg.Nickname != "" {
		nickCh <- cfg.Nickname
	}

	model := ui.NewWithChannels(cfg.Nickname, sendCh, nickCh, connectCh).WithVersion(version)
	prog := tea.NewProgram(model, tea.WithAltScreen())

	go startCoreAfterNickname(ctx, svc, nickCh, sendCh, connectCh, prog, cfg, cfgPath)
	go forwardCoreEvents(svc.Events(), prog)

	if _, err := prog.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	cancel()
	_ = svc.Stop()
}

func startCoreAfterNickname(
	ctx context.Context,
	svc *core.Service,
	nickCh <-chan string,
	sendCh <-chan core.SendRequest,
	connectCh <-chan string,
	prog *tea.Program,
	cfg *config.Config,
	cfgPath string,
) {
	var nickname string
	select {
	case nickname = <-nickCh:
	case <-ctx.Done():
		return
	}
	cfg.Nickname = nickname
	_ = cfg.Save(cfgPath)

	go func() {
		if err := svc.Start(ctx); err != nil {
			prog.Send(ui.MsgError{Err: err})
		}
	}()

	for {
		select {
		case req := <-sendCh:
			go func(r core.SendRequest) {
				if err := svc.Send(ctx, r); err != nil {
					prog.Send(ui.MsgError{Err: err})
				}
			}(req)
		case addr := <-connectCh:
			go func(a string) {
				var err error
				if strings.HasPrefix(a, "/") {
					err = svc.Connect(ctx, a)
				} else {
					err = svc.ConnectByNickname(ctx, a)
				}
				if err != nil {
					prog.Send(ui.MsgError{Err: err})
				}
			}(addr)
		case <-ctx.Done():
			return
		}
	}
}

func runUpdate() {
	fmt.Printf("현재 버전: %s\n", version)
	fmt.Print("최신 버전 확인 중... ")

	newTag, err := update.Do(version)
	if errors.Is(err, update.ErrAlreadyLatest) {
		fmt.Println("이미 최신 버전입니다.")
		return
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "실패: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("%s 업데이트 완료!\n다시 실행하면 새 버전이 적용됩니다.\n", newTag)
}

var debugLogFile *os.File

func initDebugLog() error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	path := filepath.Join(cwd, "bdpeer-debug.log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	debugLogFile = f
	debugLogger = log.New(f, "", log.LstdFlags|log.Lmicroseconds)
	fmt.Fprintf(os.Stderr, "debug 모드: %s 에 로그를 기록합니다.\n", path)
	// Redirect fd 2 to the log file so Go runtime fatal errors and panics
	// (which write directly to stderr) survive the TUI alt-screen takeover.
	if err := redirectStderrToFile(f); err != nil {
		debugLogger.Printf("[debug] stderr redirect 실패: %v", err)
	}
	return nil
}

func closeDebugLog() {
	if debugLogFile != nil {
		debugLogger.Printf("=== bdpeer stopped (%s) ===", time.Now().Format(time.RFC3339))
		_ = debugLogFile.Close()
	}
}

func debugEvent(ev core.Event) {
	if debugLogger == nil {
		return
	}
	switch ev.Type {
	case core.EventLog:
		debugLogger.Printf("[log] %s", ev.Content)
	case core.EventPeerFound:
		debugLogger.Printf("[peer_found] id=%s nick=%s src=%s", ev.Peer.ID, ev.Peer.Nickname, ev.Peer.Source)
	case core.EventPeerLost:
		debugLogger.Printf("[peer_lost] id=%s", ev.Peer.ID)
	case core.EventTextReceived:
		debugLogger.Printf("[text] from=%s content=%q", ev.From, ev.Content)
	case core.EventFileStart:
		debugLogger.Printf("[file_start] from=%s name=%s size=%d outgoing=%v", ev.From, ev.Name, ev.Size, ev.Outgoing)
	case core.EventFileProgress:
		debugLogger.Printf("[file_progress] from=%s %d/%d outgoing=%v", ev.From, ev.Received, ev.Total, ev.Outgoing)
	case core.EventFileDone:
		debugLogger.Printf("[file_done] from=%s name=%s path=%s outgoing=%v", ev.From, ev.Name, ev.Path, ev.Outgoing)
	case core.EventError:
		debugLogger.Printf("[error] %v", ev.Err)
	case core.EventReady:
		debugLogger.Printf("[ready] addr=%s", ev.LocalAddr)
	default:
		debugLogger.Printf("[%s] %+v", ev.Type, ev)
	}
}

func forwardCoreEvents(events <-chan core.Event, prog *tea.Program) {
	for ev := range events {
		debugEvent(ev)
		switch ev.Type {
		case core.EventPeerFound:
			prog.Send(ui.MsgPeerFound{Info: ev.Peer})
		case core.EventPeerLost:
			prog.Send(ui.MsgPeerLost{ID: ev.Peer.ID})
		case core.EventTextReceived:
			prog.Send(ui.MsgTextReceived{From: ev.From, Content: ev.Content})
		case core.EventFileStart:
			prog.Send(ui.MsgFileStart{From: ev.From, Name: ev.Name, Size: ev.Size, Outgoing: ev.Outgoing})
		case core.EventFileProgress:
			prog.Send(ui.MsgFileProgress{From: ev.From, Received: ev.Received, Total: ev.Total, Outgoing: ev.Outgoing})
		case core.EventFileDone:
			prog.Send(ui.MsgFileDone{From: ev.From, Name: ev.Name, SavePath: ev.Path, Outgoing: ev.Outgoing})
		case core.EventError:
			prog.Send(ui.MsgError{Err: ev.Err})
		case core.EventReady:
			prog.Send(ui.MsgLocalAddr{Addr: ev.LocalAddr})
		case core.EventLog:
			prog.Send(ui.MsgLog{Text: ev.Content})
		}
	}
}
