-- ワークスペースの参加者（オーナー・入室したプレイヤー）。「自分のワークスペース」の一覧に使う
CREATE TABLE IF NOT EXISTS workspace_members (
  workspace_id    CHAR(26)    NOT NULL,
  player_id       CHAR(26)    NOT NULL,
  role            TINYINT     NOT NULL,            -- 0=member 1=owner
  first_joined_at DATETIME(3) NOT NULL,
  last_joined_at  DATETIME(3) NOT NULL,
  PRIMARY KEY (workspace_id, player_id),
  KEY idx_workspace_members_player (player_id, last_joined_at),
  CONSTRAINT fk_workspace_members_workspace FOREIGN KEY (workspace_id) REFERENCES workspaces (id),
  CONSTRAINT fk_workspace_members_player FOREIGN KEY (player_id) REFERENCES players (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- 既存のオーナーを埋める
INSERT IGNORE INTO workspace_members (workspace_id, player_id, role, first_joined_at, last_joined_at)
SELECT id, owner_id, 1, created_at, created_at FROM workspaces;
