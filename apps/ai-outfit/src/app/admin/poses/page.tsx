'use client';

import { useEffect, useState, useCallback } from 'react';
import { Table, Space, Input, Tag, Button, message } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { adminRequest, adminMutation } from '../_components/admin-request';
import { PageHeader } from '../_components/page-header';
import { useResponsivePagination } from '../_components/responsive-pagination';

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
  const [loading, setLoading] = useState(true);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(20);
  const pagination = useResponsivePagination({
    current: page, pageSize, total,
    onChange: (p, ps) => { setPage(p); setPageSize(ps); },
  });
  const [keyword, setKeyword] = useState('');

  const fetchData = useCallback(() => adminRequest<{ data: Row[]; total: number }>(
    `/api/admin/poses?page=${page}&limit=${pageSize}&keyword=${encodeURIComponent(keyword)}`,
  ), [page, pageSize, keyword]);

  useEffect(() => {
    let active = true;
    fetchData()
      .then((json) => {
        if (!active) return;
        setRows(json.data ?? []);
        setTotal(json.total ?? 0);
      })
      .catch(() => { if (active) message.error('加载失败'); })
      .finally(() => { if (active) setLoading(false); });
    return () => { active = false; };
  }, [fetchData]);

  const reload = () => {
    setLoading(true);
    fetchData()
      .then((json) => { setRows(json.data ?? []); setTotal(json.total ?? 0); })
      .catch(() => message.error('加载失败'))
      .finally(() => setLoading(false));
  };

  const handleToggleActive = async (r: Row) => {
    if (await adminMutation(`/api/admin/poses?id=${r.id}`, 'PATCH', { is_active: !r.is_active })) {
      message.success(r.is_active ? '已下架' : '已上架');
      reload();
    }
  };

  const columns: ColumnsType<Row> = [
    { title: '名称', dataIndex: 'name_zh', key: 'name_zh', ellipsis: true },
    { title: '体系', dataIndex: 'system', key: 'system', responsive: ['lg'] },
    { title: '分类', dataIndex: 'category', key: 'category', render: (v) => <Tag>{v}</Tag> },
    { title: '角度', dataIndex: 'angle', key: 'angle', responsive: ['md'], render: (v) => <Tag color="blue">{v}</Tag> },
    { title: '创建时间', dataIndex: 'created_at', key: 'created_at', responsive: ['lg'], render: (v) => new Date(v).toLocaleString() },
    {
      title: '操作', key: 'op', fixed: 'right', width: 120, render: (_, r) => (
        <Space>
          <Button size="small" onClick={() => handleToggleActive(r)}>{r.is_active ? '下架' : '上架'}</Button>
        </Space>
      ),
    },
  ];

  return (
    <div>
      <PageHeader
        title="姿势管理"
        extra={
          <Input.Search placeholder="搜索姿势" className="w-full sm:w-64" onSearch={(value) => { setKeyword(value); setPage(1); }} />
        }
      />
      <Table
        rowKey="id"
        columns={columns}
        dataSource={rows}
        loading={loading}
        scroll={{ x: 'max-content' }}
        pagination={pagination}
      />
    </div>
  );
}
