package store

import (
	"context"
	"path/filepath"
	"testing"

	"go.mau.fi/util/dbutil"
	"maunium.net/go/mautrix/crypto"
	"maunium.net/go/mautrix/sqlstatestore"
)

// The bot shares one SQLite file with mautrix's state and crypto stores; make sure
// their schema upgrades accept our tables (and ours accept theirs on restart).
func TestSharedWithMautrixStores(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "shared.db")
	for i := 0; i < 2; i++ {
		s, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := sqlstatestore.NewSQLStateStore(s.DB, dbutil.NoopLogger, false).Upgrade(ctx); err != nil {
			t.Fatalf("state store upgrade: %v", err)
		}
		cs := crypto.NewSQLCryptoStore(s.DB, dbutil.NoopLogger, "@bot:x", "DEV", []byte("key"))
		if err := cs.DB.Upgrade(ctx); err != nil {
			t.Fatalf("crypto store upgrade: %v", err)
		}
		s.Close()
	}
}
