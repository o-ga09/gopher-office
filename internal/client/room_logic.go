package client

import (
	"image"
	"net/url"
	"sort"
	"strconv"

	"github.com/o-ga09/gopher-office/internal/apiclient"
)

// このファイルは、ワークスペース・ルームの切替に使う純粋関数（描画・通信に依存しない）。

// workspacePath は招待コードのワークスペースの URL パス。
func workspacePath(code string) string { return "/w/" + url.PathEscape(code) }

// sortWorkspaces は新しく入った順（同時刻なら名前順）に並べた写しを返す。
func sortWorkspaces(ws []apiclient.MyWorkspace) []apiclient.MyWorkspace {
	out := append([]apiclient.MyWorkspace(nil), ws...)
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].LastJoinedAt.Equal(out[j].LastJoinedAt) {
			return out[i].LastJoinedAt.After(out[j].LastJoinedAt)
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// isOwner は、招待コードのワークスペースのオーナーかどうかを返す。
func isOwner(ws []apiclient.MyWorkspace, code string) bool {
	for _, w := range ws {
		if w.InviteCode == code {
			return w.Role == "owner"
		}
	}
	return false
}

// roomRow は一覧の 1 行。
type roomRow struct {
	apiclient.Room
	Current bool
}

// roomRows は一覧の並び（API の順のまま）に、現在のルームの印を付ける。
func roomRows(rooms []apiclient.Room, currentID string) []roomRow {
	rows := make([]roomRow, len(rooms))
	for i, r := range rooms {
		rows[i] = roomRow{Room: r, Current: r.ID == currentID}
	}
	return rows
}

// switchTarget は、一覧で targetID を選んだときに切り替えるべきかを返す。
// 空の ID や、今いるルームを選んだときは切り替えない。
func switchTarget(currentID, targetID string) (string, bool) {
	if targetID == "" || targetID == currentID {
		return "", false
	}
	return targetID, true
}

// nextRoomID は、ルーム選択（プロフィール画面）で次の選択肢の ID を返す。末尾の次は先頭に戻る。
func nextRoomID(rooms []apiclient.Room, selectedID string) string {
	if len(rooms) == 0 {
		return ""
	}
	for i, r := range rooms {
		if r.ID == selectedID {
			return rooms[(i+1)%len(rooms)].ID
		}
	}
	return rooms[0].ID
}

// roomName は、ID に対応するルーム名を返す。見つからなければ空。
func roomName(rooms []apiclient.Room, id string) string {
	for _, r := range rooms {
		if r.ID == id {
			return r.Name
		}
	}
	return ""
}

// rowAt は、縦に並んだ行（先頭が area.Min、高さ rowH、n 行）のうち p が指す行の添字を返す。無ければ -1。
func rowAt(p image.Point, area image.Rectangle, rowH, n int) int {
	if !p.In(area) || rowH <= 0 {
		return -1
	}
	i := (p.Y - area.Min.Y) / rowH
	if i < 0 || i >= n {
		return -1
	}
	return i
}

// nextRoomName は新しいルームの名前（n は今のルーム数）。
func nextRoomName(n int) string { return "Room " + strconv.Itoa(n+1) }
