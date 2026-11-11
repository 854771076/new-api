'use client';

import { useEffect, useState, useCallback } from 'react';
import { Table, Space, Input, Tag, Button, message } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { adminRequest, adminMutation } from '../_components/admin-request';

interface Row {
  id: string;
  name_zh: string;
  system: string;
  category: string;
  angle: string;
  is_active: boolean;
  created_at: string;
}

export default function AdminPosesPage() {
  const [rows, setRows] = useState<Row[]>([]);
  const [loading, setLoading] = useState(false);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [keyword, setKeyword] = useState('');

  const fetchData = useCallback(async () => {
    setLoading(true);
    try {
      const json = await adminRequest<{ data: Row[]; total: number }>(`/api/admin/poses?page=${page}&limit=30&keyword=${encodeURIComponent(keyword)}`);
      setRows(json.data ?? []);
      setTotal(json.total ?? 0);
    } catch { message.error('加载失败'); }
    finally { setLoading(false); }
  }, [page, keyword]);

  useEffect(() => {
    // 延迟到下一个任务执行，避免在 effect 同步阶段触发 setState
    const timer = setTimeout(() => { void fetchData(); }, 0);
    return () => clearTimeout(timer);
  }, [fetchData]);

  const handleToggleActive = async (r: Row) => {
    if (await adminMutation(`/api/admin/poses?id=${r.id}`, 'PATCH', { is_active: !r.is_active })) {
      message.success(r.is_active ? '已下架' : '已上架');
      void fetchData();
    }
  };

  const columns: ColumnsType<Row> = [
    { title: '名称', dataIndex: 'name_zh', key: 'name_zh' },
    { title: '体系', dataIndex: 'system', key: 'system' },
    { title: '分类', dataIndex: 'category', key: 'category', render: (v) => <Tag>{v}</Tag> },
    { title: '角度', dataIndex: 'angle', key: 'angle', render: (v) => <Tag color="blue">{v}</Tag> },
    { title: '创建时间', dataIndex: 'created_at', key: 'created_at', render: (v) => new Date(v).toLocaleString() },
    {
      title: '操作', key: 'op', render: (_, r) => (
        <Space>
          <Button size="small" onClick={() => handleToggleActive(r)}>{r.is_active ? '下架' : '上架'}</Button>
        </Space>
      ),
    },
  ];

  return (
    <div>
      <div className="mb-4 flex items-center justify-between">
        <h1 className="text-xl font-bold">姿势管理</h1>
        <Space>
          <Input.Search placeholder="搜索姿势" style={{ width: 240 }} onSearch={(value) => { setKeyword(value); setPage(1); }} />
        </Space>
      </div>
      <Table
        rowKey="id"
        columns={columns}
        dataSource={rows}
        loading={loading}
        pagination={{ current: page, pageSize: 30, total, showTotal: (t) => `共 ${t} 条`, onChange: setPage }}
      />
    </div>
  );
}
