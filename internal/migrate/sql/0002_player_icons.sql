-- アイコン画像（再エンコード後の 64x64 PNG）。内容のハッシュで重複を避ける
CREATE TABLE player_icons (
  hash       CHAR(64)    CHARACTER SET ascii COLLATE ascii_bin NOT NULL,  -- 画像の SHA-256（小文字の 16 進数）
  data       MEDIUMBLOB  NOT NULL,
  created_at DATETIME(3) NOT NULL,
  PRIMARY KEY (hash)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
