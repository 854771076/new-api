'use client';

import { useEffect, useState, useCallback } from 'react';
import { Table, Space, Button, Input, Tag, Popconfirm, message, Form, Modal, InputNumber, Switch } from 'antd';
import type { ColumnsType } from 'antd/es/table';

interface Row {
  id: string;
  name_zh: string;
  category: string;
  sort_order: number;
  is_active: boolean;
  created_at: string;
}

export default function AdminOutfitsPage() {
  const [rows, setRows] = useState<Row[]>([]);
  const [loading, setLoading] = useState(false);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(20);
  const [keyword, setKeyword] = useState('');
  const [modalOpen, setModalOpen] = useState(false);
  const [editing, setEditing] = useState<Row | null>(null);
  const [form] = Form.useForm();

  const fetchData = useCallback(async () => {
    setLoading(true);
    try {
      const url = `/api/admin/outfits?page=${page}&limit=${pageSize}&keyword=${encodeURIComponent(keyword)}`;
      const res = await fetch(url);
      const json = (await res.json()) as { data: Row[]; total: number };
      setRows(json.data ?? []);
      setTotal(json.total ?? 0);
    } catch {
      message.error('加载失败');
    } finally {
      setLoading(false);
    }
  }, [page, pageSize, keyword]);

  useEffect(() => {
    // 延迟到下一个任务执行，避免在 effect 同步阶段触发 setState
    const timer = setTimeout(() => { void fetchData(); }, 0);
    return () => clearTimeout(timer);
  }, [fetchData]);

  const handleNew = () => {
    setEditing(null);
    form.resetFields();
    form.setFieldsValue({ is_active: true, bucket: 'media-official', sort_order: 0 });
    setModalOpen(true);
  };
  const handleEdit = (r: Row) => {
    setEditing(r);
    form.setFieldsValue(r);
    setModalOpen(true);
  };
  const handleDelete = async (r: Row) => {
    const res = await fetch(`/api/admin/outfits?id=${r.id}`, { method: 'DELETE' });
    if (res.ok) { message.success('已删除'); fetchData(); }
  };
  const handleToggleActive = async (r: Row) => {
    const res = await fetch(`/api/admin/outfits?id=${r.id}`, {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ is_active: !r.is_active }),
    });
    if (res.ok) {
      message.success(r.is_active ? '已下架' : '已上架');
      fetchData();
    }
  };
  const handleSubmit = async () => {
    const values = await form.validateFields();
    if (editing) {
      const res = await fetch(`/api/admin/outfits?id=${editing.id}`, { method: 'PATCH', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(values) });
      if (res.ok) { message.success('已更新'); }
    } else {
      const res = await fetch('/api/admin/outfits', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(values) });
      if (res.ok) { message.success('已创建'); }
    }
    setModalOpen(false);
    fetchData();
  };

  const columns: ColumnsType<Row> = [
    { title: '名称', dataIndex: 'name_zh', key: 'name_zh' },
    { title: '分类', dataIndex: 'category', key: 'category', render: (v) => <Tag>{v}</Tag> },
    { title: '排序', dataIndex: 'sort_order', key: 'sort_order' },
    { title: '启用', dataIndex: 'is_active', key: 'is_active', render: (v) => v ? <Tag color="green">是</Tag> : <Tag color="red">否</Tag> },
    { title: '创建时间', dataIndex: 'created_at', key: 'created_at', render: (v) => new Date(v).toLocaleString() },
    {
      title: '操作', key: 'op', render: (_, r) => (
        <Space>
          <Button size="small" onClick={() => handleEdit(r)}>编辑</Button>
          <Button size="small" onClick={() => handleToggleActive(r)}>{r.is_active ? '下架' : '上架'}</Button>
          <Popconfirm title="确认删除？" onConfirm={() => handleDelete(r)}>
            <Button size="small" danger>删除</Button>
          </Popconfirm>
        </Space>
      ),
    },
  ];

  return (
    <div>
      <div className="mb-4 flex items-center justify-between gap-3">
        <h1 className="text-xl font-bold">服装管理</h1>
        <Space>
          <Input.Search
            placeholder="按名称/分类搜索"
            allowClear
            style={{ width: 260 }}
            onSearch={(v) => { setKeyword(v); setPage(1); }}
          />
          <Button type="primary" onClick={handleNew}>新建服装</Button>
        </Space>
      </div>

      <Table
        rowKey="id"
        columns={columns}
        dataSource={rows}
        loading={loading}
        pagination={{ current: page, pageSize, total, showSizeChanger: true, showTotal: (t) => `共 ${t} 条`, onChange: (p, ps) => { setPage(p); setPageSize(ps); } }}
      />

      <Modal
        title={editing ? '编辑服装' : '新建服装'}
        open={modalOpen}
        onCancel={() => setModalOpen(false)}
        onOk={handleSubmit}
        destroyOnClose
      >
        <Form form={form} layout="vertical">
          <Form.Item label="名称 (中文)" name="name_zh" rules={[{ required: true }]}>
            <Input />
          </Form.Item>
          <Form.Item label="供应商素材 ID" name="provider_id" rules={[{ required: true }]}>
            <Input placeholder="同租户内唯一，如 SWM-0042" />
          </Form.Item>
          <Form.Item label="分类" name="category" rules={[{ required: true }]}>
            <Input />
          </Form.Item>
          <Form.Item label="描述" name="description_zh">
            <Input.TextArea rows={2} />
          </Form.Item>
          <Form.Item label="存储桶" name="bucket" rules={[{ required: true }]}>
            <Input placeholder="media-official" />
          </Form.Item>
          <Form.Item label="缩略图路径" name="thumb_path" rules={[{ required: true }]}>
            <Input placeholder="nac_xxx/outfits/SWM-0042.webp" />
          </Form.Item>
          <Form.Item label="预览图路径" name="preview_path" rules={[{ required: true }]}>
            <Input placeholder="与缩略图同目录的高清图" />
          </Form.Item>
          <Form.Item label="排序" name="sort_order">
            <InputNumber min={0} />
          </Form.Item>
          <Form.Item label="启用" name="is_active" valuePropName="checked">
            <Switch />
          </Form.Item>
        </Form>
      </Modal>
    </div>
  );
}
