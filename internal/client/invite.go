package client

import (
	"context"
	"errors"

	"github.com/o-ga09/gopher-office/internal/apiclient"
	"github.com/o-ga09/gopher-office/internal/sim"
)

// joinViaAPI は API でゲストとして登録（または前回のゲストを更新）し、招待コードのワークスペースに入る。
// 前回の playerToken が使えなければ（期限切れ・削除済み）、新しいゲストを作る。
func joinViaAPI(ctx context.Context, cfg Config, name string, gender sim.Gender) (apiclient.JoinResult, error) {
	api := apiclient.New(cfg.APIBase)

	restored := false
	if cfg.Store != nil {
		if tok := cfg.Store.Load(); tok != "" {
			api.SetToken(tok)
			err := updateProfile(ctx, api, name, uint8(gender)) //nolint:gosec // 0〜2
			switch {
			case err == nil:
				restored = true
			case apiclient.IsCode(err, "unauthorized"):
				api.SetToken("") // 使えないトークンは捨てて、新しく作る
			default:
				return apiclient.JoinResult{}, err
			}
		}
	}
	if !restored {
		if _, err := api.CreateGuest(ctx, name, uint8(gender)); err != nil { //nolint:gosec // 0〜2
			return apiclient.JoinResult{}, err
		}
		if cfg.Store != nil {
			cfg.Store.Save(api.Token())
		}
	}
	return api.Join(ctx, cfg.InviteCode)
}

// updateProfile は性別を更新する。名前が指定されていなければ、前回の名前を保つ。
func updateProfile(ctx context.Context, api *apiclient.Client, name string, gender uint8) error {
	if name == "" {
		me, err := api.Me(ctx)
		if err != nil {
			return err
		}
		name = me.DisplayName
	}
	_, err := api.UpdateMe(ctx, name, gender)
	return err
}

// dialErrorText は接続の失敗を、利用者向けの文言にする。
func dialErrorText(err error) string {
	var ae *apiclient.Error
	if errors.As(err, &ae) {
		switch ae.Code {
		case "not_found":
			return "This invitation link is not valid."
		case "rate_limited":
			return "Too many attempts.\nPlease wait a minute and try again."
		case "unavailable":
			return "No room is available right now.\nPlease try again later."
		}
	}
	return "Cannot connect to the server:\n" + err.Error()
}
