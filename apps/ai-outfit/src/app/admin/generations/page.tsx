'use client';

import { useCallback, useEffect, useRef, useState } from 'react';
import { adminRequest } from '../_components/admin-request';
import { PageHeader } from '../_components/page-header';
import { useResponsivePagination } from '../_components/responsive-pagination';
import { Alert, Button, Descriptions, Drawer, Image as AntImage, Spin, Table, Tag, Typography, message } from 'antd';
import type { ColumnsType } from 'antd/es/table';

interface GenerationRow {
  id: string;
  user_id: string;
  character_id: string | null;
  outfit_ids: string[] | null;
  pose_id: string | null;
  prompt: string | null;
  model: string;
  status: 'pending' | 'succeeded' | 'failed';
  result_bucket: string | null;
  result_path: string | null;
  error: string | null;
  created_at: string;
}

const STATUS_COLOR: Record<GenerationRow['status'], string> = {
  pending: 'gold',
  succeeded: 'green',
  failed: 'red',
};

const STATUS_LABEL: Record<GenerationRow['status'], string> = {
  pending: '处理中',
  succeeded: '成功',
  failed: '失败',
};

interface GenerationReferences {
  character: { name: string; url: string | null } | null;
  outfits: { id: string; name: string; url: string | null }[];
  pose: { name: string; url: string | null } | null;
}

function StatusTag({ status }: { status: GenerationRow['status'] }) {
  return <Tag color={STATUS_COLOR[status]}>{STATUS_LABEL[status]}</Tag>;
}

export default function AdminGenerationsPage() {
  const [rows, setRows] = useState<GenerationRow[]>([]);
  const [loading, setLoading] = useState(true);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(20);
  const pagination = useResponsivePagination({
    current: page, pageSize, total,
    onChange: (nextPage, nextPageSize) => { setLoading(true); setPage(nextPage); setPageSize(nextPageSize); },
  });
  const [detail, setDetail] = useState<GenerationRow | null>(null);
  const [imageUrl, setImageUrl] = useState<string | null>(null);
  const [imageLoading, setImageLoading] = useState(false);
  const [imageError, setImageError] = useState<string | null>(null);
  const [references, setReferences] = useState<GenerationReferences | null>(null);
  const [referencesLoading, setReferencesLoading] = useState(false);
  const [referencesError, setReferencesError] = useState<string | null>(null);
  const detailRequest = useRef<AbortController | null>(null);

  useEffect(() => () => { detailRequest.current?.abort(); }, []);

  const loadRows = useCallback(() => adminRequest<{ data: GenerationRow[]; total: number }>(
    `/api/admin/outfit_generations?page=${page}&limit=${pageSize}`,
  ), [page, pageSize]);

  useEffect(() => {
    let active = true;
    loadRows()
      .then((json) => {
        if (!active) return;
        setRows(json.data ?? []);
        setTotal(json.total ?? 0);
      })
      .catch(() => { if (active) message.error('加载失败'); })
      .finally(() => { if (active) setLoading(false); });
    return () => { active = false; };
  }, [loadRows]);

  const closeDetail = () => {
    detailRequest.current?.abort();
    setDetail(null);
  };

  const openDetail = async (row: GenerationRow) => {
    detailRequest.current?.abort();
    const controller = new AbortController();
    detailRequest.current = controller;
    const { signal } = controller;
    setDetail(row);
    setImageUrl(null);
    setImageError(null);
    setImageLoading(Boolean(row.result_bucket && row.result_path));
    setReferences(null);
    setReferencesError(null);
    setReferencesLoading(true);
    void adminRequest<{ data: GenerationReferences }>(`/api/admin/generation-references?id=${row.id}`, { signal })
      .then((json) => {
        if (!json.data) throw new Error('Missing references');
        if (!signal.aborted) setReferences(json.data);
      })
      .catch(() => { if (!signal.aborted) setReferencesError('参考图加载失败，请重试'); })
      .finally(() => { if (!signal.aborted) setReferencesLoading(false); });

    if (!row.result_bucket || !row.result_path) return;

    try {
      const query = new URLSearchParams({ bucket: row.result_bucket, path: row.result_path });
      const json = await adminRequest<{ url: string }>(`/api/admin/signed-url?${query.toString()}`, { signal });
      if (!json.url) throw new Error('Missing result');
      if (!signal.aborted) setImageUrl(json.url);
    } catch {
      if (!signal.aborted) setImageError('结果图加载失败，请重试');
    } finally {
      if (!signal.aborted) setImageLoading(false);
    }
  };

  const columns: ColumnsType<GenerationRow> = [
    {
      title: '创建时间',
      dataIndex: 'created_at',
      key: 'created_at',
      width: 180,
      responsive: ['md'],
      render: (value: string) => new Date(value).toLocaleString(),
    },
    { title: '模型', dataIndex: 'model', key: 'model', width: 160, ellipsis: true },
    {
      title: '状态',
      dataIndex: 'status',
      key: 'status',
      width: 100,
      render: (value: GenerationRow['status']) => <StatusTag status={value} />,
    },
    {
      title: 'Prompt',
      dataIndex: 'prompt',
      key: 'prompt',
      ellipsis: true,
      render: (value: string | null) => value ?? '-',
    },
    {
      title: '操作',
      key: 'op',
      width: 100,
      render: (_, record) => (
        <Button size="small" onClick={() => { void openDetail(record); }}>详情</Button>
      ),
    },
  ];

  return (
    <div>
      <PageHeader title="生成记录" />

      <Table
        rowKey="id"
        columns={columns}
        dataSource={rows}
        loading={loading}
        scroll={{ x: 'max-content' }}
        pagination={pagination}
      />

      <Drawer
        title="生成记录详情"
        open={detail !== null}
        onClose={closeDetail}
        size={560}
        destroyOnHidden
      >
        {detail && (
          <div className="flex flex-col gap-4">
            <Descriptions
              column={1}
              bordered
              size="small"
              items={[
                { key: 'id', label: 'ID', children: detail.id },
                { key: 'model', label: '模型', children: detail.model },
                { key: 'status', label: '状态', children: <StatusTag status={detail.status} /> },
                { key: 'created_at', label: '创建时间', children: new Date(detail.created_at).toLocaleString() },
                { key: 'character_id', label: '人物 ID', children: detail.character_id ?? '-' },
                {
                  key: 'outfit_ids',
                  label: '服装 ID',
                  children: detail.outfit_ids?.length ? detail.outfit_ids.join('、') : '-',
                },
                { key: 'pose_id', label: '姿势 ID', children: detail.pose_id ?? '-' },
              ]}
            />

            <div>
              <Typography.Text strong>参考图</Typography.Text>
              <div className="mt-1 flex flex-col gap-2">
                {referencesLoading && <Spin size="small" />}
                {referencesError && <Alert type="warning" showIcon title={referencesError}
                  action={<Button size="small" onClick={() => { void openDetail(detail); }}>重试</Button>} />}
                {references && (
                  <div className="flex flex-wrap gap-3">
                    {references.character?.url && (
                      <div className="w-24">
                        <AntImage src={references.character.url} width={96} height={96} style={{ objectFit: 'cover' }} />
                        <div className="truncate text-xs">人物：{references.character.name}</div>
                      </div>
                    )}
                    {references.outfits.filter((o) => o.url).map((o) => (
                      <div key={o.id} className="w-24">
                        <AntImage src={o.url as string} width={96} height={96} style={{ objectFit: 'cover' }} />
                        <div className="truncate text-xs">服装：{o.name}</div>
                      </div>
                    ))}
                    {references.pose?.url && (
                      <div className="w-24">
                        <AntImage src={references.pose.url} width={96} height={96} style={{ objectFit: 'cover' }} />
                        <div className="truncate text-xs">姿势：{references.pose.name}</div>
                      </div>
                    )}
                  </div>
                )}
                {!referencesLoading &&
                  references &&
                  !references.character?.url &&
                  !references.pose?.url &&
                  references.outfits.every((o) => !o.url) && (
                    <Typography.Text type="secondary">未找到参考图</Typography.Text>
                  )}
              </div>
            </div>

            <div>
              <Typography.Text strong>Prompt</Typography.Text>
              <pre className="mt-1 whitespace-pre-wrap break-words rounded bg-gray-50 p-3 text-xs">
                {detail.prompt || '-'}
              </pre>
            </div>

            {detail.status === 'failed' && (
              <Alert type="error" showIcon title="生成失败" description={detail.error ?? '无错误信息'} />
            )}

            {detail.result_bucket && detail.result_path && (
              <div>
                <Typography.Text strong>生成结果</Typography.Text>
                <div className="mt-1">
                  {imageLoading && <Spin />}
                  {imageError && <Alert type="warning" showIcon title={imageError}
                    action={<Button size="small" onClick={() => { void openDetail(detail); }}>重试</Button>} />}
                  {imageUrl && <AntImage src={imageUrl} alt="生成结果" style={{ maxWidth: '100%' }} />}
                </div>
              </div>
            )}
          </div>
        )}
      </Drawer>
    </div>
  );
}
