package client

import (
	"errors"
	"strings"

	"github.com/o-ga09/gopher-office/internal/apiclient"
)

// パスワードの長さ（バイト）。サーバ（internal/auth）の範囲と同じ。
const (
	minPasswordLen = 8
	maxPasswordLen = 128
	maxEmailLen    = 254
)

// lang は表示言語。
type lang int

const (
	langEN lang = iota
	langJA
)

// normalizeEmail は入力のメールアドレスを整える（前後の空白を除く）。
func normalizeEmail(s string) string { return strings.TrimSpace(s) }

// validateEmail は、メールアドレスの形（local@domain）だけを確認する。"" なら問題なし。
// 厳密な検証はサーバが行う。返すのは accountMessage に渡すコード。
func validateEmail(email string) string {
	at := strings.LastIndex(email, "@")
	if len(email) > maxEmailLen || at <= 0 || at == len(email)-1 || strings.ContainsAny(email, " \t") {
		return "invalid_email"
	}
	return ""
}

// validatePassword はパスワードの長さを確認する。"" なら問題なし。
func validatePassword(pw string) string {
	if len(pw) < minPasswordLen || len(pw) > maxPasswordLen {
		return "password_length"
	}
	return ""
}

// accountMessages はエラーコードごとの文言（英語, 日本語）。
var accountMessages = map[string][2]string{
	"invalid_email":       {"Enter a valid email address.", "メールアドレスの形式が正しくありません。"},
	"password_length":     {"Password must be 8-128 characters.", "パスワードは 8〜128 文字にしてください。"},
	"invalid_credentials": {"Email or password is incorrect.", "メールアドレスかパスワードが違います。"},
	"email_taken":         {"This email is already registered. Try signing in.", "このメールアドレスは登録済みです。サインインしてください。"},
	"already_registered":  {"This account already has an email and password.", "このアカウントはすでに登録済みです。"},
	"no_credentials":      {"This account has no password yet. Create an account first.", "このアカウントにはまだパスワードがありません。先にアカウントを作成してください。"},
	"unauthorized":        {"Your session has expired. Please sign in again.", "セッションの期限が切れました。もう一度サインインしてください。"},
	"rate_limited":        {"Too many attempts. Please wait a moment.", "試行回数が多すぎます。しばらく待ってください。"},
	"unavailable":         {"The server is unavailable. Try again later.", "サーバに接続できません。後でもう一度お試しください。"},
	"invalid_request":     {"The input is invalid. Check the email and password.", "入力内容が正しくありません。メールアドレスとパスワードを確認してください。"},
}

// accountMessage はエラーコードを文言に変換する。未知のコードには汎用の文言を返す。
func accountMessage(code string, l lang) string {
	m, ok := accountMessages[code]
	if !ok {
		m = [2]string{"Something went wrong. Please try again.", "エラーが発生しました。もう一度お試しください。"}
	}
	return m[l]
}

// accountErrorMessage は API のエラーを利用者向けの文言にする。API エラーでなければ（通信失敗など）unavailable 扱い。
func accountErrorMessage(err error, l lang) string {
	var ae *apiclient.Error
	if errors.As(err, &ae) {
		return accountMessage(ae.Code, l)
	}
	return accountMessage("unavailable", l)
}
