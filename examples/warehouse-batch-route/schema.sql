CREATE TABLE public.pallet_tasks (
    batch_no BIGINT PRIMARY KEY,
    start_command_id TEXT NOT NULL,
    pallet_started_command_id TEXT,
    material_finished_command_id TEXT,
    empty_started_command_id TEXT,
    pallet_finished_command_id TEXT,
    status TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE public.tray_records (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    batch_no BIGINT NOT NULL,
    tray_no INTEGER NOT NULL,
    tray_type TEXT NOT NULL CHECK (tray_type IN ('material', 'empty')),
    rack_no TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL CHECK (status IN ('started', 'success', 'failed')),
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX tray_records_batch_phase_idx
    ON public.tray_records (batch_no, tray_type, status);
