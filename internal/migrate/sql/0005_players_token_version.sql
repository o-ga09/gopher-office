-- playerToken の失効用。パスワード変更で上げ、古い ver のトークンを拒否する。
-- MySQL には ADD COLUMN IF NOT EXISTS が無いので、information_schema で確認して再実行しても失敗しないようにする
SET @stmt = (SELECT IF(COUNT(*) = 0, 'ALTER TABLE players ADD COLUMN token_version INT UNSIGNED NOT NULL DEFAULT 0', 'DO 0') FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = 'players' AND column_name = 'token_version');
PREPARE s FROM @stmt;
EXECUTE s;
DEALLOCATE PREPARE s;
