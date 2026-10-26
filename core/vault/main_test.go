package vault

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	kdfFloorOn = false // the tests build vaults with KDFTest (1 MiB, 1 iteration) on purpose
	os.Exit(m.Run())
}
