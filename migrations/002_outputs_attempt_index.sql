CREATE INDEX outputs_attempt_token ON outputs ((split_part(path, '/', 2)));
