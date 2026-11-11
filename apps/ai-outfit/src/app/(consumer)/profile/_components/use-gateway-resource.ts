'use client';

import { useEffect, useState } from 'react';
import type { z } from 'zod';
import { gatewayRequest } from './profile-data';

export function useGatewayResource<T>(path: string, schema: z.ZodType<T>, revision = 0) {
  const key = `${path}:${revision}`;
  const [result, setResult] = useState<{ key: string; data: T | null; error: string | null }>({
    key: '', data: null, error: null,
  });

  useEffect(() => {
    const controller = new AbortController();
    gatewayRequest(path, schema, { signal: controller.signal }).then(
      (data) => {
        if (!controller.signal.aborted) setResult({ key, data, error: null });
      },
      (error: unknown) => {
        if (!controller.signal.aborted) {
          setResult({ key, data: null, error: error instanceof Error ? error.message : '网络异常，请重试' });
        }
      },
    );
    return () => controller.abort();
  }, [path, schema, key]);

  const loading = result.key !== key;
  return { loading, data: loading ? null : result.data, error: loading ? null : result.error };
}
