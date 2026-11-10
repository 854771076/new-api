'use client';

import { useEffect } from 'react';
import { useRouter } from 'next/navigation';

export default function AdminPlaceholder() {
  const router = useRouter();
  useEffect(() => { router.replace('/admin/outfits'); }, [router]);
  return null;
}
