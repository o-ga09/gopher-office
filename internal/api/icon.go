package api

import (
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/labstack/echo/v5"

	"github.com/o-ga09/gopher-office/internal/icon"
)

const (
	// 同じハッシュを指す URL（?h=<icon_hash>）は内容が変わらないので、長くキャッシュできる。
	cacheImmutable = "public, max-age=31536000, immutable"
	// ハッシュ無しの URL は、アイコンの変更に追従できるよう、毎回 ETag で確認させる。
	cacheRevalidate = "public, no-cache"
)

// putIcon は自分のアイコン画像を差し替える。ボディは画像のバイト列そのもの（PNG / JPEG / WebP、256 KB まで）。
// 形式は Content-Type ではなく中身で判定し、64x64 の PNG に再エンコードして保存する（docs/design.md §8）。
func (h *handler) putIcon(c *echo.Context) error {
	// 上限を 1 バイト超えて読み、超過を検出する（BodyLimit が先に 413 を返すので、通常は到達しない）
	data, err := io.ReadAll(io.LimitReader(c.Request().Body, icon.MaxUploadBytes+1))
	if err != nil {
		var sc echo.HTTPStatusCoder
		if errors.As(err, &sc) {
			return err // 413 など
		}
		return badRequest("could not read the request body")
	}

	pngData, hash, err := icon.Process(data)
	switch {
	case errors.Is(err, icon.ErrTooLarge):
		return newAPIError(http.StatusRequestEntityTooLarge, "too_large", "the image must be 256 KB or smaller")
	case errors.Is(err, icon.ErrDimensions):
		return newAPIError(http.StatusBadRequest, "image_dimensions", "the image must be 4096x4096 pixels or smaller")
	case errors.Is(err, icon.ErrUnsupported):
		return newAPIError(http.StatusUnsupportedMediaType, "unsupported_image", "the image must be a PNG, JPEG, or WebP file")
	case err != nil:
		return err
	}

	p, err := h.cfg.Store.SetIcon(c.Request().Context(), playerID(c), hash, pngData)
	if errors.Is(err, ErrNotFound) {
		return errUnauthorized
	}
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, map[string]any{"player": viewOf(p)})
}

// getIcon はプレイヤーのアイコン画像（64x64 PNG）を返す。認証は不要（<img> や CDN から取れるように）。
// ETag は icon_hash。クエリの h が現在のハッシュと一致するときだけ、長期キャッシュを許可する。
func (h *handler) getIcon(c *echo.Context) error {
	id := c.Param("id")
	if len(id) != 26 || !isAlnum(id) {
		return errNotFound
	}
	hash, data, err := h.cfg.Store.GetIcon(c.Request().Context(), id)
	if errors.Is(err, ErrNotFound) {
		return errNotFound
	}
	if err != nil {
		return err
	}

	etag := `"` + hash + `"`
	hd := c.Response().Header()
	hd.Set("ETag", etag)
	hd.Set("X-Content-Type-Options", "nosniff")
	if c.QueryParam("h") == hash {
		hd.Set("Cache-Control", cacheImmutable)
	} else {
		hd.Set("Cache-Control", cacheRevalidate)
	}
	if etagMatches(c.Request().Header.Get("If-None-Match"), etag) {
		return c.NoContent(http.StatusNotModified)
	}
	return c.Blob(http.StatusOK, "image/png", data)
}

func etagMatches(header, etag string) bool {
	for _, t := range strings.Split(header, ",") {
		if t = strings.TrimSpace(t); t == etag || t == "*" {
			return true
		}
	}
	return false
}

func isAlnum(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') {
			return false
		}
	}
	return true
}
