package memory_test

import (
	"testing"

	"github.com/xraph/ledger/store/storetest"
)

func TestConformance(t *testing.T) { storetest.Run(t, storetest.NewMemory) }
