package appversion_test

import (
	"fmt"
	"sync"
	"testing"

	chassis "github.com/ai8future/chassis-go/v11"
	"github.com/ai8future/chassis-go/v11/internal/appversion"
)

func init() {
	chassis.RequireMajor(11)
}

func TestGetReturnsEmptyBeforeSet(t *testing.T) {
	if got := appversion.Get(); got != "" {
		t.Fatalf("Get() = %q, want empty", got)
	}
}

func TestSetPreservesExactVersion(t *testing.T) {
	appversion.Set(" 1.2.3-rc.1 ")
	if got := appversion.Get(); got != " 1.2.3-rc.1 " {
		t.Fatalf("Get() = %q", got)
	}
}

func TestSetAndGetAreSafeDuringConcurrentUse(t *testing.T) {
	const workers = 32
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			appversion.Set(fmt.Sprintf("1.2.%d", i))
			_ = appversion.Get()
		}(i)
	}
	wg.Wait()
	if appversion.Get() == "" {
		t.Fatal("concurrent Set left an empty version")
	}
}
