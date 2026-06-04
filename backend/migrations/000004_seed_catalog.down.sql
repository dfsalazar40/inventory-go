-- Remove seed catalog items (reverse of 000004_seed_catalog.up.sql).
DELETE FROM items WHERE id IN (
    'item-vintage-camera',
    'item-mechanical-watch',
    'item-acoustic-guitar',
    'item-smart-flask',
    'item-running-shoes',
    'item-gaming-mouse'
);
