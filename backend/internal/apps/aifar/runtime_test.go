package aifar

import (
	"errors"
	"testing"
)

func TestAggregateServiceActionFailuresReportsFirstRealCause(t *testing.T) {
	cause := errors.New("AIFAR service contacts is not installed")
	err := aggregateServiceActionFailures("zh", []serviceActionFailure{
		{service: "contacts", err: cause},
		{service: "file", err: errors.New("AIFAR service file is not installed")},
	})
	if err == nil {
		t.Fatal("aggregate error is nil")
	}
	const want = "部分 AIFAR Runtime 服务操作失败：contacts,file；首个原因：AIFAR service contacts is not installed"
	if err.Error() != want {
		t.Fatalf("aggregate error=%q, want %q", err.Error(), want)
	}
	if !errors.Is(err, cause) {
		t.Fatal("aggregate error no longer unwraps the original cause")
	}
}
