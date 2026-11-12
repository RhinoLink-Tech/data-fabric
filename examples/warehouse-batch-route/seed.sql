INSERT INTO public.pallet_tasks (
    batch_no,
    start_command_id,
    pallet_started_command_id,
    material_finished_command_id,
    empty_started_command_id,
    pallet_finished_command_id,
    status,
    created_at
) VALUES
    (9001, 'synthetic-start-9001', 'synthetic-pallet-9001',
     'synthetic-material-finished-9001', 'synthetic-empty-9001',
     'synthetic-pallet-finished-9001', 'COMPLETED',
     TIMESTAMPTZ '2026-11-07 08:00:00+00'),
    (9002, 'synthetic-start-9002', NULL, NULL, NULL, NULL,
     'PENDING', TIMESTAMPTZ '2026-11-07 08:30:00+00');

INSERT INTO public.tray_records (
    batch_no, tray_no, tray_type, rack_no, status, started_at, finished_at, created_at
) VALUES
    (9001, 4, 'material', 'SYNTHETIC-STATION-03', 'success',
     TIMESTAMPTZ '2026-11-07 08:05:00+00',
     TIMESTAMPTZ '2026-11-07 08:10:00+00',
     TIMESTAMPTZ '2026-11-07 08:05:00+00'),
    (9001, 1, 'empty', 'SYNTHETIC-STATION-03', 'success',
     TIMESTAMPTZ '2026-11-07 08:20:00+00',
     TIMESTAMPTZ '2026-11-07 08:25:00+00',
     TIMESTAMPTZ '2026-11-07 08:20:00+00');
