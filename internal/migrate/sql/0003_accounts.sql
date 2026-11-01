-- 永続アカウント（メール + パスワード）。ゲストが昇格すると行ができる（player_id は変わらない）
CREATE TABLE IF NOT EXISTS player_credentials (
  player_id     CHAR(26)     NOT NULL,
  -- 保存前に trim + 小文字化する。bin 照合で、その結果が一意になる
  email         VARCHAR(254) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
  password_hash VARCHAR(255) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,  -- argon2id の PHC 形式
  created_at    DATETIME(3)  NOT NULL,
  updated_at    DATETIME(3)  NOT NULL,
  PRIMARY KEY (player_id),
  UNIQUE KEY uq_player_credentials_email (email),
  CONSTRAINT fk_player_credentials_player FOREIGN KEY (player_id) REFERENCES players (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
