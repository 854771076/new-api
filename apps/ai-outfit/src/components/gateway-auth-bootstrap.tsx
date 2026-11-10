'use client';

import { useEffect } from 'react';
import { ensureGatewayAuthListener } from '@/lib/gatewayAuth';

export function GatewayAuthBootstrap() {
  useEffect(() => { ensureGatewayAuthListener(); }, []);
  return null;
}
