'use client';

import { useEffect, useState } from 'react';
import { Alert, Button, Table, Tag } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { adminRequest } from '../_components/admin-request';

interface CharacterRow {
  id: string;
  name: string;
  description: string | null;
  user_id: string;
  is_public: boolean;
  created_at: string;
}

const columns: ColumnsType<CharacterRow> = [
  { title: '名称', dataIndex: 'name' },
  { title: '用户 ID', dataIndex: 'user_id' },
  { title: '描述', dataIndex: 'description' },
  { title: '公开', dataIndex: 'is_public', render: (value: boolean) => <Tag>{value ? '是' : '否'}</Tag> },
  { title: '创建时间', dataIndex: 'created_at', render: (value: string) => new Date(value).toLocaleString() },
];

export default function AdminCharactersPage() {
  const [rows, setRows] = useState<CharacterRow[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(false);
  const [reloadKey, setReloadKey] = useState(0);
  useEffect(() => {
    let active = true;
    adminRequest<{ data: CharacterRow[]; total: number }>(`/api/admin/characters?page=${page}&limit=20`)
      .then((data) => {
        if (!active) return;
        setRows(data.data);
        setTotal(data.total);
        setError(false);
      })
      .catch(() => { if (active) setError(true); })
      .finally(() => { if (active) setLoading(false); });
    return () => { active = false; };
  }, [page, reloadKey]);
  return (
    <div>
      <h1 className="mb-4 text-xl font-bold">人物管理（只读）</h1>
      {error && <Alert type="error" showIcon title="加载失败" action={<Button onClick={() => { setLoading(true); setReloadKey((value) => value + 1); }}>重试</Button>} />}
      <Table rowKey="id" columns={columns} dataSource={rows} loading={loading}
        pagination={{ current: page, pageSize: 20, total, onChange: (value) => { setLoading(true); setPage(value); } }} />
    </div>
  );
}
