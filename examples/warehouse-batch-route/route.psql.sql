SELECT
    p.batch_no,
    CASE
        WHEN p.status IN ('PENDING', 'SENDING', 'ACCEPTED', 'RUNNING', 'PAUSED')
         AND p.pallet_started_command_id IS NULL
         AND p.empty_started_command_id IS NULL
         AND t.id IS NULL
        THEN 'WAIT'
        ELSE 'OUT_OF_SCOPE'
    END AS route_origin,
    CASE
        WHEN p.status IN ('PENDING', 'SENDING', 'ACCEPTED', 'RUNNING', 'PAUSED')
         AND p.pallet_started_command_id IS NULL
         AND p.empty_started_command_id IS NULL
         AND t.id IS NULL
        THEN 'FULL_PALLET'
        ELSE NULL
    END AS next_destination
FROM public.pallet_tasks AS p
LEFT JOIN public.tray_records AS t
    ON t.batch_no = p.batch_no
   AND t.tray_type IN ('material', 'empty')
   AND t.status = 'started'
WHERE p.batch_no = :'batch_no'::BIGINT
LIMIT 1;
