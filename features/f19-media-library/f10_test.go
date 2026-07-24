package medialibrary

import (
	"context"
	"testing"
)

type f10CommandsStub struct {
	commands []F10QuotaCommand
	accepted bool
}

func (stub *f10CommandsStub) ApplyQuota(
	_ context.Context,
	command F10QuotaCommand,
) (F10QuotaDecision, error) {
	stub.commands = append(stub.commands, command)
	return F10QuotaDecision{Accepted: stub.accepted, Code: "accepted"}, nil
}

func TestF10AdapterUsesServerOwnedResourceAndSignedDelta(t *testing.T) {
	commands := &f10CommandsStub{accepted: true}
	quota, err := NewF10MediaQuota(commands)
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := quota.ReserveMediaBytes(
		context.Background(), "workspace-1", 2048, "reserve-1",
	)
	if err != nil || !accepted {
		t.Fatalf("accepted = %v, error = %v", accepted, err)
	}
	if err := quota.ReleaseMediaBytes(
		context.Background(), "workspace-1", 2048, "release-1",
	); err != nil {
		t.Fatal(err)
	}
	if len(commands.commands) != 2 ||
		commands.commands[0].Resource != QuotaResource ||
		commands.commands[0].Delta != 2048 ||
		commands.commands[1].Delta != -2048 {
		t.Fatalf("commands = %#v", commands.commands)
	}
}
