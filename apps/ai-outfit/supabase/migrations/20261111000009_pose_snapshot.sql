-- 20261111000009_pose_snapshot.sql
-- 换装姿势参考快照：任务创建时冻结所选姿势的 bucket + preview_path，
-- 恢复任务时按快照取图，不读取可能已变更的现表。仅 Supabase Postgres；可重复执行。

alter table public.outfit_generations
  add column if not exists pose_snapshot jsonb;
