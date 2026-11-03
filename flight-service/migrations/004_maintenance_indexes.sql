-- Indexes for retention cleanup and faster FK maintenance on jobs/collection_runs.

CREATE INDEX IF NOT EXISTS idx_collection_runs_job_id
    ON collection_runs (job_id)
    WHERE job_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_collection_runs_created_at
    ON collection_runs (created_at);

CREATE INDEX IF NOT EXISTS idx_jobs_done_finished_at
    ON jobs (finished_at)
    WHERE status = 'done';

CREATE INDEX IF NOT EXISTS idx_jobs_dead_finished_at
    ON jobs (finished_at)
    WHERE status = 'dead';
