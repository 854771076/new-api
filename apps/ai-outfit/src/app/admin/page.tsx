'use client';

import { useEffect, useState } from 'react';
import { useRouter } from 'next/navigation';
import { Alert, Button, Card, Col, Row, Spin, Statistic, Table } from 'antd';
import type { ColumnsType } from 'antd/es/table';

interface RecentFailure {
  id: string;
  model: string;
  prompt: string | null;
  error: string | null;
  created_at: string;
}

interface StatsData {
  generations_total: number;
  generations_today: number;
  generations_succeeded: number;
  generations_failed: number;
  success_rate: number;
  outfits_active: number;
  poses_active: number;
  characters_total: number;
  recent_failures: RecentFailure[];
}

interface StatCard {
  key: string;
  title: string;
  value: number;
  suffix?: string;
  precision?: number;
}

export default function AdminOverviewPage() {
  const router = useRouter();
  const [stats, setStats] = useState<StatsData | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [reloadKey, setReloadKey] = useState(0);

  useEffect(() => {
    let active = true;
    fetch('/api/admin/stats')
      .then(async (res) => {
        const json = (await res.json()) as { data?: StatsData; error?: string };
        if (!res.ok || !json.data) throw new Error(json.error ?? `请求失败 (${res.status})`);
        return json.data;
      })
      .then((data) => {
        if (!active) return;
        setStats(data);
        setError(null);
      })
      .catch((e: unknown) => {
        if (active) setError(e instanceof Error ? e.message : '加载失败');
      })
      .finally(() => {
        if (active) setLoading(false);
      });
    return () => { active = false; };
  }, [reloadKey]);

  const handleRetry = () => {
    setLoading(true);
    setReloadKey((key) => key + 1);
  };

  const primaryCards: StatCard[] = stats
    ? [
        { key: 'total', title: '生成总数', value: stats.generations_total },
        { key: 'today', title: '今日生成', value: stats.generations_today },
        { key: 'rate', title: '成功率', value: stats.success_rate, suffix: '%', precision: 1 },
        { key: 'failed', title: '失败数', value: stats.generations_failed },
      ]
    : [];

  const secondaryCards: StatCard[] = stats
    ? [
        { key: 'outfits', title: '在售服装', value: stats.outfits_active },
        { key: 'poses', title: '在售姿势', value: stats.poses_active },
        { key: 'characters', title: '人物总数', value: stats.characters_total },
        { key: 'succeeded', title: '生成成功数', value: stats.generations_succeeded },
      ]
    : [];

  const failureColumns: ColumnsType<RecentFailure> = [
    { title: '模型', dataIndex: 'model', key: 'model' },
    { title: '错误', dataIndex: 'error', key: 'error', render: (value: string | null) => value ?? '-' },
    {
      title: '时间',
      dataIndex: 'created_at',
      key: 'created_at',
      render: (value: string) => new Date(value).toLocaleString(),
    },
  ];

  return (
    <div>
      <h1 className="mb-4 text-xl font-bold">运营概览</h1>

      {error && (
        <Alert
          type="error"
          showIcon
          className="mb-4"
          message="统计数据加载失败"
          description={error}
          action={<Button size="small" onClick={handleRetry}>重试</Button>}
        />
      )}

      <Spin spinning={loading}>
        {stats && (
          <>
            <Row gutter={[16, 16]}>
              {primaryCards.map((card) => (
                <Col key={card.key} xs={24} sm={12} lg={6}>
                  <Card>
                    <Statistic title={card.title} value={card.value} suffix={card.suffix} precision={card.precision} />
                  </Card>
                </Col>
              ))}
            </Row>

            <Row gutter={[16, 16]} className="mt-4">
              {secondaryCards.map((card) => (
                <Col key={card.key} xs={24} sm={12} lg={6}>
                  <Card>
                    <Statistic title={card.title} value={card.value} suffix={card.suffix} precision={card.precision} />
                  </Card>
                </Col>
              ))}
            </Row>

            <Card className="mt-4" title="最近失败记录">
              <Table
                rowKey="id"
                size="small"
                columns={failureColumns}
                dataSource={stats.recent_failures}
                pagination={false}
                onRow={() => ({
                  onClick: () => router.push('/admin/generations'),
                  style: { cursor: 'pointer' },
                })}
              />
            </Card>
          </>
        )}
      </Spin>
    </div>
  );
}
