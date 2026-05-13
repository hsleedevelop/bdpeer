package main

import (
	"context"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/chad/bdpeer/internal/config"
	"github.com/chad/bdpeer/internal/core"
	"github.com/chad/bdpeer/internal/ui"
)

func main() {
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
	if cfg.Nickname != "" {
		nickCh <- cfg.Nickname
	}

	model := ui.NewWithChannels(cfg.Nickname, sendCh, nickCh)
	prog := tea.NewProgram(model, tea.WithAltScreen())

	go startCoreAfterNickname(ctx, svc, nickCh, sendCh, prog, cfg, cfgPath)
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
		case <-ctx.Done():
			return
		}
	}
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
		}
	}
}
