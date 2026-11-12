import fs from 'node:fs';
import path from 'node:path';

const localesDir =
  'D:/workplace/code/ai-dev/new-api-monster/web/src/i18n/locales';

// Translations for the 15 untranslated concurrency keys, per locale.
const translations = {
  'zh-TW': {
    Concurrency: '並發',
    'Per-key and per-user in-flight request counts. Refreshes every 3 seconds.':
      '每個金鑰與使用者的進行中請求計數。每 3 秒重新整理。',
    'Active concurrency buckets': '活躍並發儲存桶',
    'Total in-flight requests': '進行中請求總數',
    'Live count across every active key and user concurrency bucket.':
      '跨所有活躍金鑰與使用者並發儲存桶的即時計數。',
    'Configured concurrency caps': '已設定並發上限',
    'Sum of explicit per-key / per-user caps (unlimited scopes excluded).':
      '明確的每金鑰 / 每使用者上限總和（不含無限範圍）。',
    'Average utilisation': '平均使用率',
    'Mean of capped-bucket utilisation. 0% means no caps are currently in use.':
      '有上限儲存桶使用率的平均值。0% 表示目前沒有使用任何上限。',
    'Over-limit scopes': '超出上限的範圍',
    'Number of concurrency buckets currently exceeding their configured cap.':
      '目前超過設定上限的並發儲存桶數量。',
    'Current / limit': '目前 / 上限',
    Utilisation: '使用率',
    'No active concurrency buckets right now.':
      '目前沒有活躍的並發儲存桶。',
    unlimited: '無限制',
  },
  fr: {
    Concurrency: 'Concurrence',
    'Per-key and per-user in-flight request counts. Refreshes every 3 seconds.':
      "Nombre de requêtes en cours par clé et par utilisateur. Actualisation toutes les 3 secondes.",
    'Active concurrency buckets': 'Bacs de concurrence actifs',
    'Total in-flight requests': 'Requêtes en cours (total)',
    'Live count across every active key and user concurrency bucket.':
      "Compteur en direct pour tous les bacs de concurrence actifs (clés et utilisateurs).",
    'Configured concurrency caps': 'Plafonds de concurrence configurés',
    'Sum of explicit per-key / per-user caps (unlimited scopes excluded).':
      "Somme des plafonds explicites par clé / par utilisateur (portées illimitées exclues).",
    'Average utilisation': 'Utilisation moyenne',
    'Mean of capped-bucket utilisation. 0% means no caps are currently in use.':
      "Utilisation moyenne des bacs plafonnés. 0 % signifie qu'aucun plafond n'est actuellement utilisé.",
    'Over-limit scopes': 'Portées au-delà du plafond',
    'Number of concurrency buckets currently exceeding their configured cap.':
      'Nombre de bacs de concurrence dépassant actuellement leur plafond configuré.',
    'Current / limit': 'Actuel / limite',
    Utilisation: 'Utilisation',
    'No active concurrency buckets right now.':
      'Aucun bac de concurrence actif pour le moment.',
    unlimited: 'illimité',
  },
  ru: {
    Concurrency: 'Конкурентность',
    'Per-key and per-user in-flight request counts. Refreshes every 3 seconds.':
      'Счётчик выполняющихся запросов по каждому ключу и пользователю. Обновление каждые 3 секунды.',
    'Active concurrency buckets': 'Активные счётчики конкурентности',
    'Total in-flight requests': 'Всего выполняющихся запросов',
    'Live count across every active key and user concurrency bucket.':
      'Живой счётчик по всем активным ключам и пользовательским счётчикам.',
    'Configured concurrency caps': 'Настроенные лимиты конкурентности',
    'Sum of explicit per-key / per-user caps (unlimited scopes excluded).':
      'Сумма явных лимитов на ключ / пользователя (без неограниченных областей).',
    'Average utilisation': 'Средняя загрузка',
    'Mean of capped-bucket utilisation. 0% means no caps are currently in use.':
      'Средняя загрузка ограниченных счётчиков. 0% означает, что лимиты сейчас не используются.',
    'Over-limit scopes': 'Области сверх лимита',
    'Number of concurrency buckets currently exceeding their configured cap.':
      'Число счётчиков конкурентности, превышающих настроенный лимит.',
    'Current / limit': 'Текущий / лимит',
    Utilisation: 'Загрузка',
    'No active concurrency buckets right now.':
      'Активных счётчиков конкурентности нет.',
    unlimited: 'без лимита',
  },
  ja: {
    Concurrency: '並行性',
    'Per-key and per-user in-flight request counts. Refreshes every 3 seconds.':
      'キーごと・ユーザーごとの処理中リクエスト数。3秒ごとに更新されます。',
    'Active concurrency buckets': 'アクティブな並行バケット',
    'Total in-flight requests': '処理中リクエスト合計',
    'Live count across every active key and user concurrency bucket.':
      'すべてのアクティブなキー・ユーザー並行バケットのリアルタイム件数。',
    'Configured concurrency caps': '設定済みの並行上限',
    'Sum of explicit per-key / per-user caps (unlimited scopes excluded).':
      'キー別・ユーザー別の明示的な上限の合計（無制限スコープを除く）。',
    'Average utilisation': '平均使用率',
    'Mean of capped-bucket utilisation. 0% means no caps are currently in use.':
      '上限付きバケットの使用率の平均。0% は現在どの上限も使用されていないことを示します。',
    'Over-limit scopes': '上限超過スコープ',
    'Number of concurrency buckets currently exceeding their configured cap.':
      '現在設定された上限を超えている並行バケットの数。',
    'Current / limit': '現在 / 上限',
    Utilisation: '使用率',
    'No active concurrency buckets right now.':
      '現在アクティブな並行バケットはありません。',
    unlimited: '無制限',
  },
  vi: {
    Concurrency: 'Đồng thời',
    'Per-key and per-user in-flight request counts. Refreshes every 3 seconds.':
      'Số yêu cầu đang xử lý theo từng khóa và từng người dùng. Làm mới mỗi 3 giây.',
    'Active concurrency buckets': 'Các bucket đồng thời đang hoạt động',
    'Total in-flight requests': 'Tổng yêu cầu đang xử lý',
    'Live count across every active key and user concurrency bucket.':
      'Đếm thời gian thực trên mọi bucket đồng thời của khóa và người dùng đang hoạt động.',
    'Configured concurrency caps': 'Giới hạn đồng thời đã cấu hình',
    'Sum of explicit per-key / per-user caps (unlimited scopes excluded).':
      'Tổng giới hạn tường minh theo khóa / người dùng (loại trừ phạm vi không giới hạn).',
    'Average utilisation': 'Mức sử dụng trung bình',
    'Mean of capped-bucket utilisation. 0% means no caps are currently in use.':
      'Mức sử dụng trung bình của các bucket có giới hạn. 0% nghĩa là hiện chưa giới hạn nào được dùng.',
    'Over-limit scopes': 'Phạm vi vượt giới hạn',
    'Number of concurrency buckets currently exceeding their configured cap.':
      'Số bucket đồng thời hiện đang vượt giới hạn đã cấu hình.',
    'Current / limit': 'Hiện tại / giới hạn',
    Utilisation: 'Mức sử dụng',
    'No active concurrency buckets right now.':
      'Hiện không có bucket đồng thời nào hoạt động.',
    unlimited: 'không giới hạn',
  },
};

for (const [lang, entries] of Object.entries(translations)) {
  const file = path.join(localesDir, `${lang}.json`);
  const data = JSON.parse(fs.readFileSync(file, 'utf8'));
  let changed = 0;
  for (const [key, value] of Object.entries(entries)) {
    if (data.translation[key] === undefined) {
      console.log(`${lang}: MISSING key, cannot translate: ${key}`);
      continue;
    }
    data.translation[key] = value;
    changed++;
  }
  fs.writeFileSync(file, JSON.stringify(data, null, 2) + '\n', 'utf8');
  console.log(`${lang}: updated ${changed} keys`);
}
