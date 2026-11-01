package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/o-ga09/gopher-office/internal/blob"
)

const iconMigratePage = 100

// IconMigrateResult は MigrateIcons の集計。
type IconMigrateResult struct {
	Scanned  int // 走査した行数
	Put      int // blob に書いた（dry-run なら書く予定の）数
	Mismatch int // sha256(data) != hash でスキップした数
}

// MigrateIcons は旧 data（player_icons）を blob に移す（段階 B）。hash をキーにしたページングで 100 件ずつ走査し、
// sha256(data) が hash と一致するものだけ Put する。Put は冪等なので、何度実行してもよい。
func MigrateIcons(ctx context.Context, src IconLister, dst blob.Store, dryRun bool, logf func(format string, args ...any)) (IconMigrateResult, error) {
	var res IconMigrateResult
	after := ""
	for {
		page, err := src.ListIcons(ctx, after, iconMigratePage)
		if err != nil {
			return res, fmt.Errorf("list icons after %q: %w", after, err)
		}
		for _, ic := range page {
			res.Scanned++
			sum := sha256.Sum256(ic.Data)
			if hex.EncodeToString(sum[:]) != ic.Hash {
				res.Mismatch++
				if logf != nil {
					logf("skip %s: sha256 of data does not match the hash", ic.Hash)
				}
				continue
			}
			if !dryRun {
				if err := dst.Put(ctx, IconBlobKey(ic.Hash), ic.Data, "image/png"); err != nil {
					return res, fmt.Errorf("put %s: %w", ic.Hash, err)
				}
			}
			res.Put++
		}
		if len(page) < iconMigratePage {
			return res, nil
		}
		after = page[len(page)-1].Hash
	}
}
