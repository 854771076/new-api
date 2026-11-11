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
  model_id: string;
  display_name: string;
  provider: string | null;
  usage: string;
  description_zh: string | null;
  default_size: string | null;
  default_quality: string | null;
  price_quota: number;
  is_active: boolean;
  is_default: boolean;
  sort_order: number;
  created_at: string;
  updated_at: string;
}

interface GatewayModel {
  id: string;
  owned_by?: string;
}

const USAGE_OPTIONS = [
  { value: 'image', label: '生图模型 (image)' },
  { value: 'vision', label: '视觉 LLM (vision)' },
];

const USAGE_LABELS: Record<string, { label: string; color: string }> = {
  image: { label: '生图模型', color: 'blue' },
  vision: { label: '视觉 LLM', color: 'purple' },
};

export default function AdminModelsPage() {
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
  const [gatewayModels, setGatewayModels] = useState<GatewayModel[]>([]);
  const [modelListError, setModelListError] = useState(false);
  const [manualModelId, setManualModelId] = useState(false);
  const [form] = Form.useForm();
  const selectedUsage = Form.useWatch('usage', form) as string | undefined;

  useEffect(() => {
    if (!modalOpen) return;
    let active = true;
    adminRequest<{ data: GatewayModel[] }>('/api/admin/gateway-models')
      .then((json) => {
        if (!active) return;
        setGatewayModels(json.data ?? []);
        setModelListError(false);
      })
      .catch(() => { if (active) { setGatewayModels([]); setModelListError(true); } });
    return () => { active = false; };
  }, [modalOpen]);

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
    setManualModelId(false);
    form.resetFields();
    form.setFieldsValue({ usage: 'image', is_active: true, sort_order: 0, price_quota: 0 });
    setModalOpen(true);
  };
  const handleEdit = (r: Row) => {
    setEditing(r);
    setManualModelId(false);
    form.setFieldsValue(r);
    setModalOpen(true);
  };
  const pickModel = (modelId: string) => {
    form.setFieldValue('model_id', modelId);
    const matched = gatewayModels.find((m) => m.id === modelId);
    if (matched?.owned_by) form.setFieldValue('provider', matched.owned_by);
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
    { title: '模型 ID', dataIndex: 'model_id', key: 'model_id', ellipsis: true },
    { title: '展示名', dataIndex: 'display_name', key: 'display_name', ellipsis: true },
    { title: '供应商', dataIndex: 'provider', key: 'provider', ellipsis: true, responsive: ['md'], render: (v) => v ?? '-' },
    { title: '用途', dataIndex: 'usage', key: 'usage', render: (v: string) => {
      const meta = USAGE_LABELS[v] ?? { label: v, color: 'default' };
      return <Tag color={meta.color}>{meta.label}</Tag>;
    } },
    { title: '默认尺寸', dataIndex: 'default_size', key: 'default_size', responsive: ['lg'], render: (v) => v ?? '-' },
    { title: '单价 (quota)', dataIndex: 'price_quota', key: 'price_quota', responsive: ['lg'], render: (v: number) => v ?? 0 },
    { title: '启用', dataIndex: 'is_active', key: 'is_active', render: (v) => v ? <Tag color="green">是</Tag> : <Tag color="red">否</Tag> },
    { title: '默认', dataIndex: 'is_default', key: 'is_default', responsive: ['md'], render: (v) => v ? <Tag color="blue">默认</Tag> : '-' },
    { title: '排序', dataIndex: 'sort_order', key: 'sort_order', responsive: ['lg'] },
    { title: '创建时间', dataIndex: 'created_at', key: 'created_at', responsive: ['md'], render: (v) => new Date(v).toLocaleString() },
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
        title="模型设置"
        extra={
          <>
            <Input.Search
              placeholder="按模型 ID/展示名搜索"
              allowClear
              className="w-full sm:w-64"
              onSearch={(v) => {
                if (v === keyword && page === 1) { void reload(); return; }
                setLoading(true); setKeyword(v); setPage(1);
              }}
            />
            <Button type="primary" onClick={handleNew}>新建模型</Button>
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
        title={editing ? '编辑模型' : '新建模型'}
        open={modalOpen}
        width={640}
        onCancel={() => { if (!saving) setModalOpen(false); }}
        onOk={handleSubmit}
        confirmLoading={saving}
        cancelButtonProps={{ disabled: saving }}
        destroyOnHidden
      >
        <Form form={form} layout="vertical">
          <Form.Item label="用途" name="usage" rules={[{ required: true }]}>
            <Select options={USAGE_OPTIONS} />
          </Form.Item>
          <Form.Item label="模型 ID" required>
            <Space orientation="vertical" className="w-full" size={8}>
              {modelListError ? (
                <Alert type="warning" showIcon title="网关模型列表暂时不可用，可切换为手动输入" />
              ) : null}
              {!manualModelId && gatewayModels.length > 0 ? (
                <Form.Item name="model_id" noStyle rules={[{ required: true, message: '请选择模型' }]}>
                  <Select
                    showSearch
                    placeholder="从网关可用模型中选择"
                    optionFilterProp="label"
                    options={gatewayModels.map((m) => ({ value: m.id, label: m.owned_by ? `${m.id}（${m.owned_by}）` : m.id }))}
                    onChange={pickModel}
                  />
                </Form.Item>
              ) : (
                <Form.Item name="model_id" noStyle rules={[{ required: true, message: '请输入模型 ID' }]}>
                  <Input placeholder="例如 gpt-image-1" />
                </Form.Item>
              )}
              <Button type="link" className="px-0" onClick={() => setManualModelId((v) => !v)}>
                {manualModelId ? '从网关模型列表选择' : '手动输入模型 ID'}
              </Button>
            </Space>
          </Form.Item>
          <Form.Item label="展示名" name="display_name" rules={[{ required: true }]}>
            <Input />
          </Form.Item>
          <Form.Item label="供应商" name="provider">
            <Input placeholder="选择网关模型后自动预填，可修改" />
          </Form.Item>
          <Form.Item label="说明 (中文)" name="description_zh">
            <Input.TextArea rows={3} />
          </Form.Item>
          <Form.Item label="默认尺寸" name="default_size">
            <Input placeholder="例如 1024x1024" disabled={selectedUsage === 'vision'} />
          </Form.Item>
          <Form.Item label="默认质量" name="default_quality">
            <Input placeholder="例如 high" />
          </Form.Item>
          <Form.Item label="单价 (quota)" name="price_quota" tooltip="换装费 = 生图模型单价；解析费 = 视觉模型单价 × 未解析件数">
            <InputNumber min={0} className="w-full" />
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
