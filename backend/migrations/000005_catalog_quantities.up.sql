-- Refresh the seed catalog to the reference quantities and add a stable display order.
-- Supersedes the 000004 quantities. Uses ON CONFLICT DO UPDATE so it also corrects
-- rows that an earlier seed already inserted (000004 used DO NOTHING).
--
-- Quantities mirror the design reference; reserved is the baseline "held by others"
-- so available = total_stock - reserved matches the mock:
--   Vintage Camera 14/20, Mechanical Watch 4/10, Acoustic Guitar 8/16,
--   Smart Flask 1/20, Running Shoes 0/12 (out of stock), Gaming Mouse 1/15.

ALTER TABLE items ADD COLUMN IF NOT EXISTS sort_order INTEGER NOT NULL DEFAULT 0;

INSERT INTO items (id, name, total_stock, reserved, sort_order) VALUES
    ('item-vintage-camera',   'Vintage Camera',   20,  6, 1),
    ('item-mechanical-watch', 'Mechanical Watch', 10,  6, 2),
    ('item-acoustic-guitar',  'Acoustic Guitar',  16,  8, 3),
    ('item-smart-flask',      'Smart Flask',      20, 19, 4),
    ('item-running-shoes',    'Running Shoes',    12, 12, 5),
    ('item-gaming-mouse',     'Gaming Mouse',     15, 14, 6)
ON CONFLICT (id) DO UPDATE
   SET name        = EXCLUDED.name,
       total_stock = EXCLUDED.total_stock,
       reserved    = EXCLUDED.reserved,
       sort_order  = EXCLUDED.sort_order;
