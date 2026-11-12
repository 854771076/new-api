'use client';

import { useEffect, useState, useCallback } from 'react';
import { Table, Space, Button, Input, Tag, Popconfirm, message, Form, Modal, InputNumber, Switch, Select, Alert } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { adminRequest, adminMutation } from '../_components/admin-request';
import { PageHeader } from '../_components/page-header';
import { useResponsivePagination } from '../_components/responsive-pagination';

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
  const pagination = useResponsivePagination({
    current: page, pageSize, total,
    onChange: (p, ps) => { setLoading(true); setPage(p); setPageSize(ps); },
  });
  const [keyword, setKeyword] = useState('');
  const [modalOpen, setModalOpen] = useState(false);
  const [saving, setSaving] = useState(false);
  const [editing, setEditing] = useState<Row | null>(null);
  const [form] = Form.useForm();

  const loadRows = useCallback(async () => {
    const url = `/api/admin/prompt_templates?page=${page}&limit=${pageSize}&keyword=${encodeURIComponent(keyword)}`;
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
    if (await adminMutation(`/api/admin/prompt_templates?id=${r.id}`, 'DELETE')) { message.success('已删除'); void reload(); }
  };
  const handleToggleActive = async (r: Row) => {
    if (await adminMutation(`/api/admin/prompt_templates?id=${r.id}`, 'PATCH', { is_active: !r.is_active })) { message.success(r.is_active ? '已停用' : '已启用'); void reload(); }
  };
  const handleSetDefault = async (r: Row) => {
    if (await adminMutation(`/api/admin/prompt_templates?id=${r.id}`, 'PATCH', { is_default: true })) { message.success('已设为默认'); void reload(); }
  };
  const handleSubmit = async () => {
    const values = await form.validateFields().catch(() => null);
    if (!values || saving) return;
    setSaving(true);
    const saved = await adminMutation(editing ? `/api/admin/prompt_templates?id=${editing.id}` : '/api/admin/prompt_templates', editing ? 'PATCH' : 'POST', values);
    setSaving(false);
    if (!saved) return;
    message.success(editing ? '已更新' : '已创建');
    setModalOpen(false);
    void reload();
  };
  const appendPlaceholder = (placeholder: string) => {
    const current = form.getFieldValue('template') as string | undefined;
    form.setFieldsValue({ template: `${current ?? ''}${placeholder}` });
  };

  const columns: ColumnsType<Row> = [
    { title: '名称', dataIndex: 'name', key: 'name', ellipsis: true },
    { title: '用途', dataIndex: 'usage', key: 'usage', render: (v) => <Tag>{v}</Tag> },
    { title: '是否默认', dataIndex: 'is_default', key: 'is_default', responsive: ['md'], render: (v) => v ? <Tag color="blue">默认</Tag> : '-' },
    { title: '启用', dataIndex: 'is_active', key: 'is_active', render: (v) => v ? <Tag color="green">是</Tag> : <Tag color="red">否</Tag> },
    { title: '排序', dataIndex: 'sort_order', key: 'sort_order', responsive: ['lg'] },
    { title: '更新时间', dataIndex: 'updated_at', key: 'updated_at', responsive: ['md'], render: (v) => new Date(v).toLocaleString() },
    {
      title: '操作', key: 'op', fixed: 'right', width: 240, render: (_, r) => (
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
      <PageHeader
        title="提示词模板"
        extra={
          <>
            <Input.Search
              placeholder="按名称搜索"
              allowClear
              className="w-full sm:w-64"
              onSearch={(v) => {
                if (v === keyword && page === 1) { void reload(); return; }
                setLoading(true); setKeyword(v); setPage(1);
              }}
            />
            <Button type="primary" onClick={handleNew}>新建模板</Button>
          </>
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

      <Modal
        title={editing ? '编辑模板' : '新建模板'}
        open={modalOpen}
        onCancel={() => { if (!saving) setModalOpen(false); }}
        onOk={handleSubmit}
        confirmLoading={saving}
        cancelButtonProps={{ disabled: saving }}
        destroyOnHidden
        width={760}
      >
        <Alert
          type="info"
          showIcon
          className="mb-4"
          title="可用占位符"
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
