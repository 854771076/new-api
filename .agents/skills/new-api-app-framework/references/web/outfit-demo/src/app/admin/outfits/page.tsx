'use client';

import { useEffect, useState, useCallback } from 'react';
import { Table, Space, Button, Input, Tag, Popconfirm, message, Form, Modal, InputNumber, Switch } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { adminRequest, adminMutation } from '../_components/admin-request';
import { PageHeader } from '../_components/page-header';
import { useResponsivePagination } from '../_components/responsive-pagination';

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
  const [loading, setLoading] = useState(true);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(20);
  const pagination = useResponsivePagination({
    current: page, pageSize, total,
    onChange: (p, ps) => { setPage(p); setPageSize(ps); },
  });
  const [keyword, setKeyword] = useState('');
  const [modalOpen, setModalOpen] = useState(false);
  const [saving, setSaving] = useState(false);
  const [editing, setEditing] = useState<Row | null>(null);
  const [form] = Form.useForm();

  const fetchData = useCallback(() => adminRequest<{ data: Row[]; total: number }>(
    `/api/admin/outfits?page=${page}&limit=${pageSize}&keyword=${encodeURIComponent(keyword)}`,
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
    if (await adminMutation(`/api/admin/outfits?id=${r.id}`, 'DELETE')) { message.success('已删除'); reload(); }
  };
  const handleToggleActive = async (r: Row) => {
    if (await adminMutation(`/api/admin/outfits?id=${r.id}`, 'PATCH', { is_active: !r.is_active })) {
      message.success(r.is_active ? '已下架' : '已上架');
      reload();
    }
  };
  const handleSubmit = async () => {
    const values = await form.validateFields().catch(() => null);
    if (!values || saving) return;
    const fields = editing ? { name_zh: values.name_zh, category: values.category, description_zh: values.description_zh, sort_order: values.sort_order, is_active: values.is_active } : values;
    setSaving(true);
    const saved = await adminMutation(editing ? `/api/admin/outfits?id=${editing.id}` : '/api/admin/outfits', editing ? 'PATCH' : 'POST', fields);
    setSaving(false);
    if (!saved) return;
    message.success(editing ? '已更新' : '已创建');
    setModalOpen(false);
    reload();
  };

  const columns: ColumnsType<Row> = [
    { title: '名称', dataIndex: 'name_zh', key: 'name_zh', ellipsis: true },
    { title: '分类', dataIndex: 'category', key: 'category', render: (v) => <Tag>{v}</Tag> },
    { title: '排序', dataIndex: 'sort_order', key: 'sort_order', responsive: ['lg'] },
    { title: '启用', dataIndex: 'is_active', key: 'is_active', responsive: ['md'], render: (v) => v ? <Tag color="green">是</Tag> : <Tag color="red">否</Tag> },
    { title: '创建时间', dataIndex: 'created_at', key: 'created_at', responsive: ['md'], render: (v) => new Date(v).toLocaleString() },
    {
      title: '操作', key: 'op', fixed: 'right', width: 200, render: (_, r) => (
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
      <PageHeader
        title="服装管理"
        extra={
          <>
            <Input.Search
              placeholder="按名称/分类搜索"
              allowClear
              className="w-full sm:w-64"
              onSearch={(v) => { setKeyword(v); setPage(1); }}
            />
            <Button type="primary" onClick={handleNew}>新建服装</Button>
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
        title={editing ? '编辑服装' : '新建服装'}
        open={modalOpen}
        onCancel={() => { if (!saving) setModalOpen(false); }}
        onOk={handleSubmit}
        confirmLoading={saving}
        cancelButtonProps={{ disabled: saving }}
        destroyOnHidden
      >
        <Form form={form} layout="vertical">
          <Form.Item label="名称 (中文)" name="name_zh" rules={[{ required: true }]}>
            <Input />
          </Form.Item>
          <Form.Item label="供应商素材 ID" name="provider_id" rules={[{ required: true }]}>
            <Input disabled={editing !== null} placeholder="同租户内唯一，如 SWM-0042" />
          </Form.Item>
          <Form.Item label="分类" name="category" rules={[{ required: true }]}>
            <Input />
          </Form.Item>
          <Form.Item label="描述" name="description_zh">
            <Input.TextArea rows={2} />
          </Form.Item>
          <Form.Item label="存储桶" name="bucket" rules={[{ required: true }]}>
            <Input disabled={editing !== null} placeholder="media-official" />
          </Form.Item>
          <Form.Item label="缩略图路径" name="thumb_path" rules={[{ required: true }]}>
            <Input disabled={editing !== null} placeholder="nac_xxx/outfits/SWM-0042.webp" />
          </Form.Item>
          <Form.Item label="预览图路径" name="preview_path" rules={[{ required: true }]}>
            <Input disabled={editing !== null} placeholder="与缩略图同目录的高清图" />
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
