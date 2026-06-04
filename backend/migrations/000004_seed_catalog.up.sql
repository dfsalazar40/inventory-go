-- Seed catalog mirroring the visual reference.
-- One out-of-stock item (Running Shoes) is intentional for testing the "0 available" state.
-- ON CONFLICT DO NOTHING makes this idempotent if re-run against an existing DB.

INSERT INTO items (id, name, total_stock, reserved) VALUES
    ('item-vintage-camera',    'Vintage Camera',    5,  0),
    ('item-mechanical-watch',  'Mechanical Watch',  3,  0),
    ('item-acoustic-guitar',   'Acoustic Guitar',   2,  0),
    ('item-smart-flask',       'Smart Flask',       10, 0),
    ('item-running-shoes',     'Running Shoes',     0,  0),
    ('item-gaming-mouse',      'Gaming Mouse',      7,  0)
ON CONFLICT (id) DO NOTHING;
