'use client';

import { useEffect, useState, useCallback } from 'react';
import { Table, Space, Button, Input, Tag, Popconfirm, message, Form, Modal, InputNumber, Switch, Select } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { adminRequest, adminMutation } from '../_components/admin-request';

interface Row {
  id: string;
  client_id: string;
  model_id: string;
  display_name: string;
  provider: string | null;
  usage: string;
  description_zh: string | null;
  default_size: string | null;
  default_quality: string | null;
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

export default function AdminModelsPage() {
  const [rows, setRows] = useState<Row[]>([]);
  const [loading, setLoading] = useState(true);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(20);
  const [keyword, setKeyword] = useState('');
  const [modalOpen, setModalOpen] = useState(false);
  const [saving, setSaving] = useState(false);
  const [editing, setEditing] = useState<Row | null>(null);
  const [form] = Form.useForm();

  const loadRows = useCallback(async () => {
    const url = `/api/admin/ai_models?page=${page}&limit=${pageSize}&keyword=${encodeURIComponent(keyword)}`;
    return adminRequest<{ data: Row[]; total: number }>(url);
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
    if (await adminMutation(`/api/admin/ai_models?id=${r.id}`, 'DELETE')) { message.success('已删除'); void reload(); }
  };
  const handleToggleActive = async (r: Row) => {
    if (await adminMutation(`/api/admin/ai_models?id=${r.id}`, 'PATCH', { is_active: !r.is_active })) { message.success(r.is_active ? '已停用' : '已启用'); void reload(); }
  };
  const handleSetDefault = async (r: Row) => {
    if (await adminMutation(`/api/admin/ai_models?id=${r.id}`, 'PATCH', { is_default: true })) { message.success('已设为默认'); void reload(); }
  };
  const handleSubmit = async () => {
    const values = await form.validateFields().catch(() => null);
    if (!values || saving) return;
    setSaving(true);
    const saved = await adminMutation(editing ? `/api/admin/ai_models?id=${editing.id}` : '/api/admin/ai_models', editing ? 'PATCH' : 'POST', values);
    setSaving(false);
    if (!saved) return;
    message.success(editing ? '已更新' : '已创建');
    setModalOpen(false);
    void reload();
  };

  const columns: ColumnsType<Row> = [
    { title: '模型 ID', dataIndex: 'model_id', key: 'model_id' },
    { title: '展示名', dataIndex: 'display_name', key: 'display_name' },
    { title: '供应商', dataIndex: 'provider', key: 'provider', render: (v) => v ?? '-' },
    { title: '用途', dataIndex: 'usage', key: 'usage', render: (v) => <Tag>{v}</Tag> },
    { title: '默认尺寸', dataIndex: 'default_size', key: 'default_size', render: (v) => v ?? '-' },
    { title: '启用', dataIndex: 'is_active', key: 'is_active', render: (v) => v ? <Tag color="green">是</Tag> : <Tag color="red">否</Tag> },
    { title: '默认', dataIndex: 'is_default', key: 'is_default', render: (v) => v ? <Tag color="blue">默认</Tag> : '-' },
    { title: '排序', dataIndex: 'sort_order', key: 'sort_order' },
    { title: '创建时间', dataIndex: 'created_at', key: 'created_at', render: (v) => new Date(v).toLocaleString() },
    {
      title: '操作', key: 'op', render: (_, r) => (
        r.client_id === '' ? <Tag>共享默认（只读）</Tag> : <Space>
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
        <h1 className="text-xl font-bold">模型设置</h1>
        <Space>
          <Input.Search
            placeholder="按模型 ID/展示名搜索"
            allowClear
            style={{ width: 260 }}
            onSearch={(v) => {
              if (v === keyword && page === 1) { void reload(); return; }
              setLoading(true); setKeyword(v); setPage(1);
            }}
          />
          <Button type="primary" onClick={handleNew}>新建模型</Button>
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
        title={editing ? '编辑模型' : '新建模型'}
        open={modalOpen}
        onCancel={() => { if (!saving) setModalOpen(false); }}
        onOk={handleSubmit}
        confirmLoading={saving}
        cancelButtonProps={{ disabled: saving }}
        destroyOnHidden
      >
        <Form form={form} layout="vertical">
          <Form.Item label="模型 ID" name="model_id" rules={[{ required: true }]}>
            <Input placeholder="例如 gpt-image-1" />
          </Form.Item>
          <Form.Item label="展示名" name="display_name" rules={[{ required: true }]}>
            <Input />
          </Form.Item>
          <Form.Item label="供应商" name="provider">
            <Input placeholder="例如 openai" />
          </Form.Item>
          <Form.Item label="用途" name="usage" rules={[{ required: true }]}>
            <Select options={USAGE_OPTIONS} />
          </Form.Item>
          <Form.Item label="说明 (中文)" name="description_zh">
            <Input.TextArea rows={3} />
          </Form.Item>
          <Form.Item label="默认尺寸" name="default_size">
            <Input placeholder="例如 1024x1024" />
          </Form.Item>
          <Form.Item label="默认质量" name="default_quality">
            <Input placeholder="例如 high" />
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
