package client

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/o-ga09/gopher-office/internal/apiclient"
	"github.com/o-ga09/gopher-office/internal/sim"
)

// joinViaAPI は API でゲストとして登録（または前回のゲストを更新）し、招待コードのワークスペースに入る。
// 前回の playerToken が使えなければ（期限切れ・削除済み）、新しいゲストを作る。
// iconPNG があれば、アイコンとしてアップロードする。返すプレイヤーは、アイコンのハッシュを含む最新のプロフィール。
func joinViaAPI(ctx context.Context, cfg Config, name string, gender sim.Gender, iconPNG []byte) (apiclient.JoinResult, apiclient.Player, error) {
	api := apiclient.New(cfg.APIBase)

	var me apiclient.Player
	restored := false
	if cfg.Store != nil {
		if tok := cfg.Store.Load(); tok != "" {
			api.SetToken(tok)
			p, err := updateProfile(ctx, api, name, uint8(gender)) //nolint:gosec // 0〜2
			switch {
			case err == nil:
				me, restored = p, true
			case apiclient.IsCode(err, "unauthorized"):
				api.SetToken("") // 使えないトークンは捨てて、新しく作る
			default:
				return apiclient.JoinResult{}, apiclient.Player{}, err
			}
		}
	}
	if !restored {
		p, err := api.CreateGuest(ctx, name, uint8(gender)) //nolint:gosec // 0〜2
		if err != nil {
			return apiclient.JoinResult{}, apiclient.Player{}, err
		}
		me = p
		if cfg.Store != nil {
			cfg.Store.Save(api.Token())
		}
	}
	if len(iconPNG) > 0 {
		p, err := api.UploadIcon(ctx, iconPNG)
		if err != nil {
			return apiclient.JoinResult{}, apiclient.Player{}, fmt.Errorf("upload icon: %w", err)
		}
		me = p
	}
	j, err := api.Join(ctx, cfg.InviteCode)
	return j, me, err
}

// updateProfile は性別を更新する。名前が指定されていなければ、前回の名前を保つ。
func updateProfile(ctx context.Context, api *apiclient.Client, name string, gender uint8) (apiclient.Player, error) {
	if name == "" {
		me, err := api.Me(ctx)
		if err != nil {
			return apiclient.Player{}, err
		}
		name = me.DisplayName
	}
	return api.UpdateMe(ctx, name, gender)
}

// tokenQuery は、接続先の URL に付けるトークンのクエリを返す（URL にすでにクエリがあれば & でつなぐ）。
func tokenQuery(serverURL, token string) string {
	sep := "?"
	if strings.Contains(serverURL, "?") {
		sep = "&"
	}
	return sep + "token=" + url.QueryEscape(token)
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
		case "unsupported_image", "image_dimensions", "too_large":
			return "The server rejected the icon image.\nPlease go back and choose a different one."
		case "unavailable":
			return "No room is available right now.\nPlease try again later."
		}
	}
	return "Cannot connect to the server:\n" + err.Error()
}
