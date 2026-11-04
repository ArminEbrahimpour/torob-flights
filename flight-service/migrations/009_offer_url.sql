-- Page on the selling site where this collected offer can be bought.
ALTER TABLE flights ADD COLUMN IF NOT EXISTS offer_url TEXT NOT NULL DEFAULT '';
