package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/hsleedevelop/bdpeer/internal/config"
	"github.com/hsleedevelop/bdpeer/internal/core"
	"github.com/hsleedevelop/bdpeer/internal/ui"
	"github.com/hsleedevelop/bdpeer/internal/update"
)

// Set at build time via ldflags.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "--version", "-v", "version":
			fmt.Printf("bdpeer %s (%s) built %s\n", version, commit, date)
			return
		case "--update", "update":
			runUpdate()
			return
		}
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

	model := ui.NewWithChannels(cfg.Nickname, sendCh, nickCh, connectCh)
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
				if err := svc.Connect(ctx, a); err != nil {
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

func forwardCoreEvents(events <-chan core.Event, prog *tea.Program) {
	for ev := range events {
		switch ev.Type {
		case core.EventPeerFound:
			prog.Send(ui.MsgPeerFound{Info: ev.Peer})
		case core.EventPeerLost:
			prog.Send(ui.MsgPeerLost{ID: ev.Peer.ID})
		case core.EventTextReceived:
			prog.Send(ui.MsgTextReceived{From: ev.From, Content: ev.Content})
		case core.EventFileStart:
			prog.Send(ui.MsgFileStart{From: ev.From, Name: ev.Name, Size: ev.Size})
		case core.EventFileProgress:
			prog.Send(ui.MsgFileProgress{From: ev.From, Received: ev.Received, Total: ev.Total})
		case core.EventFileDone:
			prog.Send(ui.MsgFileDone{From: ev.From, Name: ev.Name, SavePath: ev.Path})
		case core.EventError:
			prog.Send(ui.MsgError{Err: ev.Err})
		case core.EventReady:
			prog.Send(ui.MsgLocalAddr{Addr: ev.LocalAddr})
		}
	}
}
