'use client';

import { useEffect, useState, useCallback } from 'react';
import { Table, Space, Button, Input, Tag, Popconfirm, message, Form, Modal, InputNumber, Switch, Select, Alert } from 'antd';
import type { ColumnsType } from 'antd/es/table';

interface Row {
  id: string;
  client_id: string;
  usage: string;
  name: string;
  template: string;
  negative_prompt: string | null;
  is_active: boolean;
  is_default: boolean;
  sort_order: number;
  created_at: string;
  updated_at: string;
}

const USAGE_OPTIONS = [
  { value: 'outfit', label: '换装 (outfit)' },
  { value: 'portrait', label: '人像 (portrait)' },
  { value: 'general', label: '通用 (general)' },
];

const PLACEHOLDERS = ['{{character}}', '{{outfits}}', '{{pose}}', '{{extra}}'];

export default function AdminPromptsPage() {
  const [rows, setRows] = useState<Row[]>([]);
  const [loading, setLoading] = useState(true);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(20);
  const [keyword, setKeyword] = useState('');
  const [modalOpen, setModalOpen] = useState(false);
  const [editing, setEditing] = useState<Row | null>(null);
  const [form] = Form.useForm();

  const loadRows = useCallback(async () => {
    const url = `/api/admin/prompt_templates?page=${page}&limit=${pageSize}&keyword=${encodeURIComponent(keyword)}`;
    const res = await fetch(url);
    return (await res.json()) as { data: Row[]; total: number };
  }, [page, pageSize, keyword]);

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

  const reload = async () => {
    setLoading(true);
    try {
      const json = await loadRows();
      setRows(json.data ?? []);
      setTotal(json.total ?? 0);
    } catch {
      message.error('加载失败');
    } finally {
      setLoading(false);
    }
  };

  const handleNew = () => {
    setEditing(null);
    form.resetFields();
    form.setFieldsValue({ usage: 'outfit', is_active: true, sort_order: 0 });
    setModalOpen(true);
  };
  const handleEdit = (r: Row) => {
    setEditing(r);
    form.setFieldsValue(r);
    setModalOpen(true);
  };
  const handleDelete = async (r: Row) => {
    const res = await fetch(`/api/admin/prompt_templates?id=${r.id}`, { method: 'DELETE' });
    if (res.ok) { message.success('已删除'); void reload(); }
  };
  const handleToggleActive = async (r: Row) => {
    const res = await fetch(`/api/admin/prompt_templates?id=${r.id}`, {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ is_active: !r.is_active }),
    });
    if (res.ok) { message.success(r.is_active ? '已停用' : '已启用'); void reload(); }
  };
  const handleSetDefault = async (r: Row) => {
    const res = await fetch(`/api/admin/prompt_templates?id=${r.id}`, {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ is_default: true }),
    });
    if (res.ok) { message.success('已设为默认'); void reload(); }
  };
  const handleSubmit = async () => {
    const values = await form.validateFields();
    if (editing) {
      const res = await fetch(`/api/admin/prompt_templates?id=${editing.id}`, { method: 'PATCH', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(values) });
      if (res.ok) { message.success('已更新'); }
    } else {
      const res = await fetch('/api/admin/prompt_templates', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(values) });
      if (res.ok) { message.success('已创建'); }
    }
    setModalOpen(false);
    void reload();
  };
  const appendPlaceholder = (placeholder: string) => {
    const current = form.getFieldValue('template') as string | undefined;
    form.setFieldsValue({ template: `${current ?? ''}${placeholder}` });
  };

  const columns: ColumnsType<Row> = [
    { title: '名称', dataIndex: 'name', key: 'name' },
    { title: '用途', dataIndex: 'usage', key: 'usage', render: (v) => <Tag>{v}</Tag> },
    { title: '是否默认', dataIndex: 'is_default', key: 'is_default', render: (v) => v ? <Tag color="blue">默认</Tag> : '-' },
    { title: '启用', dataIndex: 'is_active', key: 'is_active', render: (v) => v ? <Tag color="green">是</Tag> : <Tag color="red">否</Tag> },
    { title: '排序', dataIndex: 'sort_order', key: 'sort_order' },
    { title: '更新时间', dataIndex: 'updated_at', key: 'updated_at', render: (v) => new Date(v).toLocaleString() },
    {
      title: '操作', key: 'op', render: (_, r) => (
        <Space>
          <Button size="small" onClick={() => handleEdit(r)}>编辑</Button>
          <Button size="small" onClick={() => handleToggleActive(r)}>{r.is_active ? '停用' : '启用'}</Button>
          <Button size="small" disabled={r.is_default} onClick={() => handleSetDefault(r)}>设为默认</Button>
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
        <h1 className="text-xl font-bold">提示词模板</h1>
        <Space>
          <Input.Search
            placeholder="按名称搜索"
            allowClear
            style={{ width: 260 }}
            onSearch={(v) => { setLoading(true); setKeyword(v); setPage(1); }}
          />
          <Button type="primary" onClick={handleNew}>新建模板</Button>
        </Space>
      </div>

      <Table
        rowKey="id"
        columns={columns}
        dataSource={rows}
        loading={loading}
        pagination={{ current: page, pageSize, total, showSizeChanger: true, showTotal: (t) => `共 ${t} 条`, onChange: (p, ps) => { setLoading(true); setPage(p); setPageSize(ps); } }}
      />

      <Modal
        title={editing ? '编辑模板' : '新建模板'}
        open={modalOpen}
        onCancel={() => setModalOpen(false)}
        onOk={handleSubmit}
        destroyOnClose
        width={760}
      >
        <Alert
          type="info"
          showIcon
          style={{ marginBottom: 16 }}
          message="可用占位符"
          description={
            <div className="flex flex-wrap items-center gap-2">
              <span>点击按钮把占位符追加到模板末尾：</span>
              {PLACEHOLDERS.map((p) => (
                <Button key={p} size="small" onClick={() => appendPlaceholder(p)}>{p}</Button>
              ))}
            </div>
          }
        />
        <Form form={form} layout="vertical">
          <Form.Item label="名称" name="name" rules={[{ required: true }]}>
            <Input />
          </Form.Item>
          <Form.Item label="用途" name="usage" rules={[{ required: true }]}>
            <Select options={USAGE_OPTIONS} />
          </Form.Item>
          <Form.Item label="模板" name="template" rules={[{ required: true }]}>
            <Input.TextArea rows={8} />
          </Form.Item>
          <Form.Item label="负向提示词" name="negative_prompt">
            <Input.TextArea rows={3} />
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
