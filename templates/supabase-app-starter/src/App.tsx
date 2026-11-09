import { useEffect, useState } from 'react';
import { BrowserRouter, Navigate, Route, Routes, Link } from 'react-router-dom';
import { getSupabase } from '@/lib/supabase';
import { Login } from '@/pages/Login';
import { AuthCallback } from '@/pages/AuthCallback';
import { Profile } from '@/pages/Profile';
import { AIGenerate } from '@/pages/AIGenerate';
import { History } from '@/pages/History';

function Root() {
  const [authed, setAuthed] = useState<boolean | null>(null);

  useEffect(() => {
    getSupabase().auth.onAuthStateChange((_evt, session) => {
      setAuthed(!!session?.user);
    });
    getSupabase().auth.getUser().then(({ data }) => setAuthed(!!data.user));
  }, []);

  if (authed === null) return <div className="p-6 text-center">加载中…</div>;

  return (
    <BrowserRouter>
      {authed && (
        <nav className="sticky top-0 z-10 bg-white/80 backdrop-blur border-b">
          <div className="max-w-3xl mx-auto px-6 py-3 flex gap-4 text-sm">
            <Link to="/"           className="hover:underline">AI 生成</Link>
            <Link to="/history"    className="hover:underline">历史</Link>
            <Link to="/profile"    className="hover:underline">个人中心</Link>
          </div>
        </nav>
      )}
      <Routes>
        <Route path="/login"        element={authed ? <Navigate to="/" replace /> : <Login />} />
        <Route path="/auth/callback" element={<AuthCallback />} />
        {authed ? (
          <>
            <Route path="/"        element={<AIGenerate />} />
            <Route path="/history" element={<History />} />
            <Route path="/profile" element={<Profile />} />
            <Route path="*"        element={<Navigate to="/" replace />} />
          </>
        ) : (
          <Route path="*" element={<Navigate to="/login" replace />} />
        )}
      </Routes>
    </BrowserRouter>
  );
}

export default function App() {
  return <Root />;
}
