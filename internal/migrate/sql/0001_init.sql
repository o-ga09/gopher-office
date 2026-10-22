-- プレイヤー（ゲスト）
CREATE TABLE players (
  id           CHAR(26)         NOT NULL,            -- ULID
  display_name VARCHAR(128)     NOT NULL,            -- 正規化後は 32 文字以内。バイト数に余裕を持たせる
  gender       TINYINT UNSIGNED NOT NULL,            -- 0=男性(青) 1=女性(赤) 2=その他(黄)
  icon_hash    CHAR(64)         NULL,                -- アイコン画像の SHA-256（画像本体は別テーブル）
  created_at   DATETIME(3)      NOT NULL,
  PRIMARY KEY (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- ワークスペース（招待URLが指す単位）
CREATE TABLE workspaces (
  id          CHAR(26)    NOT NULL,
  owner_id    CHAR(26)    NOT NULL,
  name        VARCHAR(64) NOT NULL,
  -- 招待コードは大文字と小文字を区別する（base62）。照合順序が ci だと別のコードが衝突する
  invite_code VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  max_players INT         NOT NULL DEFAULT 200,
  created_at  DATETIME(3) NOT NULL,
  PRIMARY KEY (id),
  UNIQUE KEY uq_workspaces_invite_code (invite_code),
  KEY idx_workspaces_owner (owner_id),
  CONSTRAINT fk_workspaces_owner FOREIGN KEY (owner_id) REFERENCES players (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- ルーム（1 つの GameServer に対応する）。v0 は 1 ワークスペースに 1 つ
CREATE TABLE rooms (
  id           CHAR(26)    NOT NULL,
  workspace_id CHAR(26)    NOT NULL,
  name         VARCHAR(64) NOT NULL,
  map_id       VARCHAR(32) NOT NULL DEFAULT 'office-1',
  PRIMARY KEY (id),
  KEY idx_rooms_workspace (workspace_id),
  CONSTRAINT fk_rooms_workspace FOREIGN KEY (workspace_id) REFERENCES workspaces (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
