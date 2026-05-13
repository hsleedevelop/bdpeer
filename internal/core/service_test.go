package core_test

import (
	"testing"

	"github.com/chad/bdpeer/internal/config"
	"github.com/chad/bdpeer/internal/core"
)

func TestNewService(t *testing.T) {
	svc := core.NewService(&config.Config{Nickname: "alice"}, "")
	if svc.Events() == nil {
		t.Fatal("expected events channel")
	}
}
