// Package wire は sim の型と proto メッセージの変換、およびフレームの符号化を担う。
package wire

import (
	"google.golang.org/protobuf/proto"

	officev1 "github.com/o-ga09/gopher-office/gen/office/v1"
	"github.com/o-ga09/gopher-office/internal/sim"
)

// GenderToProto は sim.Gender を proto の Gender に変換する。
func GenderToProto(g sim.Gender) officev1.Gender {
	switch g {
	case sim.GenderMale:
		return officev1.Gender_GENDER_MALE
	case sim.GenderFemale:
		return officev1.Gender_GENDER_FEMALE
	default:
		return officev1.Gender_GENDER_OTHER
	}
}

// GenderFromProto は proto の Gender を sim.Gender に変換する。未知の値は「その他」になる。
func GenderFromProto(g officev1.Gender) sim.Gender {
	switch g {
	case officev1.Gender_GENDER_MALE:
		return sim.GenderMale
	case officev1.Gender_GENDER_FEMALE:
		return sim.GenderFemale
	default:
		return sim.GenderOther
	}
}

// StateOf は Actor の現在の位置と移動の意図を MoveState にする。
func StateOf(id string, a *sim.Actor) *officev1.MoveState {
	dx, dy := a.Dir()
	s := &officev1.MoveState{
		PlayerId: id,
		X:        int32(a.X), //nolint:gosec // 座標はマップ内（数万サブユニット以下）で int32 に収まる
		Y:        int32(a.Y), //nolint:gosec // 同上
		DirX:     int32(dx),  //nolint:gosec // 方向入力は -1〜1
		DirY:     int32(dy),  //nolint:gosec // 同上
	}
	if t, ok := a.Target(); ok {
		s.HasTarget, s.TargetX, s.TargetY = true, int32(t.X), int32(t.Y) //nolint:gosec // タイル座標は小さい
	}
	return s
}

// ActorFromState は MoveState から、同じ位置・同じ意図の Actor を再現する。
// クライアントはこの Actor を sim で進めて、他人の移動を再生する。
//
// 経路は「その位置からの A*」で決まるため、同じ軌跡を再現できるのは、サーバがコマンドを適用した
// 直後（Step の前）の状態から作ったときだけ。移動の途中の状態から作ると、現在のタイルの中心へ
// 戻る別の経路になる。途中の状態（Sync）には SameIntent で意図を比べ、位置だけを補正する。
func ActorFromState(m *sim.Map, s *officev1.MoveState) *sim.Actor {
	a := sim.NewActorAt(int(s.GetX()), int(s.GetY()))
	switch {
	case s.GetDirX() != 0 || s.GetDirY() != 0:
		a.SetDir(int(s.GetDirX()), int(s.GetDirY()))
	case s.GetHasTarget():
		a.MoveTo(m, sim.Point{X: int(s.GetTargetX()), Y: int(s.GetTargetY())})
	}
	return a
}

// SameIntent は Actor の移動の意図（方向入力・クリック移動の目的地）が、MoveState と一致するかを返す。
func SameIntent(a *sim.Actor, s *officev1.MoveState) bool {
	dx, dy := a.Dir()
	if dx != int(s.GetDirX()) || dy != int(s.GetDirY()) {
		return false
	}
	t, ok := a.Target()
	if ok != s.GetHasTarget() {
		return false
	}
	return !ok || (t.X == int(s.GetTargetX()) && t.Y == int(s.GetTargetY()))
}

// Marshal はメッセージを WebSocket のバイナリフレームに符号化する。
func Marshal(m proto.Message) ([]byte, error) { return proto.Marshal(m) }

// UnmarshalServer はサーバ→クライアントのフレームを復号する。
func UnmarshalServer(b []byte) (*officev1.ServerMessage, error) {
	var m officev1.ServerMessage
	if err := proto.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// UnmarshalClient はクライアント→サーバのフレームを復号する。
func UnmarshalClient(b []byte) (*officev1.ClientMessage, error) {
	var m officev1.ClientMessage
	if err := proto.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return &m, nil
}
