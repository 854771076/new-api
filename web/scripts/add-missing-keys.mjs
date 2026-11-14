import fs from 'node:fs/promises'
import path from 'node:path'

const LOCALES_DIR = path.resolve('src/i18n/locales')

function stableStringify(obj) {
  return `${JSON.stringify(obj, null, 2)}\n`
}

// Time-window controls for the concurrency trend chart (live / presets /
// custom range) plus their empty states; retired copy replaced by FIX#1 /
// FIX#3 wording in the one-screen /concurrency refactor.
const RETIRED_KEYS = [
  'Peak / limit',
  'Per-key and per-user in-flight request counts. Refreshes every 3 seconds.',
  'Number of concurrency buckets currently exceeding their configured cap.',
]

const newKeys = {
  en: {
    // One-screen /concurrency refactor (Variant E): header, verdict band,
    // scope-table over-cap badge + sort hint, EWMA live badge.
    'In-flight request counts per key, per user, and per channel. Refreshes every 3 seconds.':
      'In-flight request counts per key, per user, and per channel. Refreshes every 3 seconds.',
    'KPI, trend and buckets follow this window':
      'KPI, trend and buckets follow this window',
    'Action needed — {{n}} buckets over cap':
      'Action needed — {{n}} buckets over cap',
    'All buckets within caps': 'All buckets within caps',
    'Backend degraded': 'Backend degraded',
    'Backend healthy': 'Backend healthy',
    'Over-limit {{n}}': 'Over-limit {{n}}',
    'Rejected {{n}}': 'Rejected {{n}}',
    'Queued {{n}}': 'Queued {{n}}',
    'Rejections observed in this window':
      'Rejections observed in this window',
    'Requests waiting in the bounded queue':
      'Requests waiting in the bounded queue',
    'Number of per-key / per-user buckets currently exceeding their configured cap.':
      'Number of per-key / per-user buckets currently exceeding their configured cap.',
    'Sorted by utilisation, hottest first':
      'Sorted by utilisation, hottest first',
    '{{n}} over cap': '{{n}} over cap',
    'Live · 3s poll': 'Live · 3s poll',
    'Auto-refreshing': 'Auto-refreshing',
    Live: 'Live',
    'Last hour': 'Last hour',
    'Last 6 hours': 'Last 6 hours',
    'Last 24 hours': 'Last 24 hours',
    'Last 3 days': 'Last 3 days',
    'Last 7 days': 'Last 7 days',
    'Custom range': 'Custom range',
    'No samples in this window.': 'No samples in this window.',
    'Pick a start and end time.': 'Pick a start and end time.',
    'Peak in-flight': 'Peak in-flight',
    'Average in-flight': 'Average in-flight',
    'Active share': 'Active share',
    'Highest in-flight requests in the selected window.':
      'Highest in-flight requests in the selected window.',
    'Average in-flight requests across the selected window.':
      'Average in-flight requests across the selected window.',
    'Share of sampled time with at least one request in flight.':
      'Share of sampled time with at least one request in flight.',
    'Failed to load this window.': 'Failed to load this window.',
    'Buckets in the window': 'Buckets in the window',
    // Bounded wait-queue settings card + live queued counter (/concurrency).
    'Bounded wait queue': 'Bounded wait queue',
    'When a concurrency bucket is full, requests wait in a bounded queue instead of failing immediately. Queue depth = min(depth ratio × concurrency limit, depth cap). A request that waits longer than the max wait, or finds the queue full, receives a 429 response.':
      'When a concurrency bucket is full, requests wait in a bounded queue instead of failing immediately. Queue depth = min(depth ratio × concurrency limit, depth cap). A request that waits longer than the max wait, or finds the queue full, receives a 429 response.',
    'Enable queueing': 'Enable queueing',
    'When disabled, requests beyond the concurrency limit are rejected immediately with 429.':
      'When disabled, requests beyond the concurrency limit are rejected immediately with 429.',
    'Depth ratio': 'Depth ratio',
    'Queue depth before capping: this multiple of the concurrency limit. Allowed values: 0 to 64.':
      'Queue depth before capping: this multiple of the concurrency limit. Allowed values: 0 to 64.',
    'Depth cap': 'Depth cap',
    'Upper bound on queued requests per bucket. Allowed values: 1 to 1024.':
      'Upper bound on queued requests per bucket. Allowed values: 1 to 1024.',
    'Max wait': 'Max wait',
    'How long a request may wait before it is rejected with 429. Allowed values: 0 to 60.':
      'How long a request may wait before it is rejected with 429. Allowed values: 0 to 60.',
    'Allowed values: 0 to 64': 'Allowed values: 0 to 64',
    'Allowed values: 1 to 1024': 'Allowed values: 1 to 1024',
    'Allowed values: 0 to 60': 'Allowed values: 0 to 60',
    'Enter a whole number': 'Enter a whole number',
    'Save queue settings': 'Save queue settings',
  },
  zh: {
    // One-screen /concurrency refactor (Variant E): header, verdict band,
    // scope-table over-cap badge + sort hint, EWMA live badge.
    'In-flight request counts per key, per user, and per channel. Refreshes every 3 seconds.':
      '按密钥、按用户、按渠道统计的在途请求计数。每 3 秒刷新一次。',
    'KPI, trend and buckets follow this window':
      'KPI、趋势和桶列表均跟随此时间窗口',
    'Action needed — {{n}} buckets over cap': '需要处理 — {{n}} 个桶超出上限',
    'All buckets within caps': '所有桶均在上限内',
    'Backend degraded': '后端降级',
    'Backend healthy': '后端正常',
    'Over-limit {{n}}': '超限 {{n}}',
    'Rejected {{n}}': '已拒绝 {{n}}',
    'Queued {{n}}': '排队中 {{n}}',
    'Rejections observed in this window': '本窗口内出现并发拒绝',
    'Requests waiting in the bounded queue': '请求正在有界队列中等待',
    'Number of per-key / per-user buckets currently exceeding their configured cap.':
      '当前超出配置上限的每密钥 / 每用户桶数量。',
    'Sorted by utilisation, hottest first': '按利用率排序，最热优先',
    '{{n}} over cap': '{{n}} 个超限',
    'Live · 3s poll': '实时 · 3 秒轮询',
    'Auto-refreshing': '自动刷新',
    Live: '实时',
    'Last hour': '最近1小时',
    'Last 6 hours': '最近6小时',
    'Last 24 hours': '最近24小时',
    'Last 3 days': '最近3天',
    'Last 7 days': '最近7天',
    'Custom range': '自定义时段',
    'No samples in this window.': '此时段没有采样数据。',
    'Pick a start and end time.': '请选择起止时间。',
    'Peak in-flight': '峰值在途数',
    'Average in-flight': '平均在途数',
    'Active share': '活跃占比',
    'Highest in-flight requests in the selected window.':
      '所选窗口内观测到的最高在途请求数。',
    'Average in-flight requests across the selected window.':
      '所选窗口内的平均在途请求数。',
    'Share of sampled time with at least one request in flight.':
      '采样时段中至少有一个在途请求的时间占比。',
    'Failed to load this window.': '该时段加载失败（可能登录已过期）。',
    'Buckets in the window': '时段内的桶',
    // Bounded wait-queue settings card + live queued counter (/concurrency).
    'Bounded wait queue': '有界等待队列',
    'When a concurrency bucket is full, requests wait in a bounded queue instead of failing immediately. Queue depth = min(depth ratio × concurrency limit, depth cap). A request that waits longer than the max wait, or finds the queue full, receives a 429 response.':
      '当并发桶已满时,请求会进入有界等待队列排队,而不是立即失败。排队深度 = min(深度倍数 × 并发限制, 深度上限)。等待超过最长时间、或队列已满的请求将收到 429 响应。',
    'Enable queueing': '启用排队',
    'When disabled, requests beyond the concurrency limit are rejected immediately with 429.':
      '关闭后,超过并发限制的请求会立即被拒绝并返回 429。',
    'Depth ratio': '深度倍数',
    'Queue depth before capping: this multiple of the concurrency limit. Allowed values: 0 to 64.':
      '截断前的排队深度:并发限制的该倍数。允许值:0 到 64。',
    'Depth cap': '深度上限',
    'Upper bound on queued requests per bucket. Allowed values: 1 to 1024.':
      '每个桶排队请求数的上限。允许值:1 到 1024。',
    'Max wait': '最长等待时间',
    'How long a request may wait before it is rejected with 429. Allowed values: 0 to 60.':
      '请求在被拒绝并返回 429 前最多可以等待的时间。允许值:0 到 60。',
    'Allowed values: 0 to 64': '允许值:0 到 64',
    'Allowed values: 1 to 1024': '允许值:1 到 1024',
    'Allowed values: 0 to 60': '允许值:0 到 60',
    'Enter a whole number': '请输入整数',
    'Save queue settings': '保存排队设置',
  },
  'zh-TW': {
    // One-screen /concurrency refactor (Variant E): header, verdict band,
    // scope-table over-cap badge + sort hint, EWMA live badge.
    'In-flight request counts per key, per user, and per channel. Refreshes every 3 seconds.':
      '按金鑰、按使用者、按渠道統計的在途請求計數。每 3 秒重新整理一次。',
    'KPI, trend and buckets follow this window':
      'KPI、趨勢和桶清單皆跟隨此時間視窗',
    'Action needed — {{n}} buckets over cap': '需要處理 — {{n}} 個桶超出上限',
    'All buckets within caps': '所有桶皆在上限內',
    'Backend degraded': '後端降級',
    'Backend healthy': '後端正常',
    'Over-limit {{n}}': '超限 {{n}}',
    'Rejected {{n}}': '已拒絕 {{n}}',
    'Queued {{n}}': '排隊中 {{n}}',
    'Rejections observed in this window': '本時間視窗內出現並發拒絕',
    'Requests waiting in the bounded queue': '請求正在有界佇列中等待',
    'Number of per-key / per-user buckets currently exceeding their configured cap.':
      '目前超出設定上限的每金鑰 / 每使用者桶數量。',
    'Sorted by utilisation, hottest first': '依利用率排序，最熱優先',
    '{{n}} over cap': '{{n}} 個超限',
    'Live · 3s poll': '即時 · 3 秒輪詢',
    'Auto-refreshing': '自動刷新',
    Live: '即時',
    'Last hour': '最近1小時',
    'Last 6 hours': '最近6小時',
    'Last 24 hours': '最近24小時',
    'Last 3 days': '最近3天',
    'Last 7 days': '最近7天',
    'Custom range': '自訂時段',
    'No samples in this window.': '此時段沒有採樣資料。',
    'Pick a start and end time.': '請選擇起訖時間。',
    'Peak in-flight': '峰值在途數',
    'Average in-flight': '平均在途數',
    'Active share': '活躍占比',
    'Highest in-flight requests in the selected window.':
      '所選視窗內觀測到的最高在途請求數。',
    'Average in-flight requests across the selected window.':
      '所選視窗內的平均在途請求數。',
    'Share of sampled time with at least one request in flight.':
      '採樣時段中至少有一個在途請求的時間佔比。',
    'Failed to load this window.': '該時段載入失敗（可能登入已過期）。',
    'Buckets in the window': '時段內的桶',
    // Bounded wait-queue settings card + live queued counter (/concurrency).
    'Bounded wait queue': '有界等待佇列',
    'When a concurrency bucket is full, requests wait in a bounded queue instead of failing immediately. Queue depth = min(depth ratio × concurrency limit, depth cap). A request that waits longer than the max wait, or finds the queue full, receives a 429 response.':
      '當並發桶已滿時,請求會進入有界等待佇列排隊,而不是立即失敗。排隊深度 = min(深度倍數 × 並發限制, 深度上限)。等待超過最長時間、或佇列已滿的請求將收到 429 響應。',
    'Enable queueing': '啟用排隊',
    'When disabled, requests beyond the concurrency limit are rejected immediately with 429.':
      '關閉後,超過並發限制的請求會立即被拒絕並回傳 429。',
    'Depth ratio': '深度倍數',
    'Queue depth before capping: this multiple of the concurrency limit. Allowed values: 0 to 64.':
      '截斷前的排隊深度:並發限制的該倍數。允許值:0 到 64。',
    'Depth cap': '深度上限',
    'Upper bound on queued requests per bucket. Allowed values: 1 to 1024.':
      '每個桶排隊請求數的上限。允許值:1 到 1024。',
    'Max wait': '最長等待時間',
    'How long a request may wait before it is rejected with 429. Allowed values: 0 to 60.':
      '請求在被拒絕並回傳 429 前最多可以等待的時間。允許值:0 到 60。',
    'Allowed values: 0 to 64': '允許值:0 到 64',
    'Allowed values: 1 to 1024': '允許值:1 到 1024',
    'Allowed values: 0 to 60': '允許值:0 到 60',
    'Enter a whole number': '請輸入整數',
    'Save queue settings': '儲存排隊設定',
  },
  fr: {
    // One-screen /concurrency refactor (Variant E): header, verdict band,
    // scope-table over-cap badge + sort hint, EWMA live badge.
    'In-flight request counts per key, per user, and per channel. Refreshes every 3 seconds.':
      'Comptage des requêtes en cours par clé, par utilisateur et par canal. Actualisation toutes les 3 secondes.',
    'KPI, trend and buckets follow this window':
      'Les KPI, la tendance et les bacs suivent cette fenêtre',
    'Action needed — {{n}} buckets over cap':
      'Action requise — {{n}} bacs au-dessus de la limite',
    'All buckets within caps': 'Tous les bacs sont dans les limites',
    'Backend degraded': 'Backend dégradé',
    'Backend healthy': 'Backend sain',
    'Over-limit {{n}}': 'Hors limite {{n}}',
    'Rejected {{n}}': 'Rejeté {{n}}',
    'Queued {{n}}': "En file d'attente {{n}}",
    'Rejections observed in this window':
      'Rejets de concurrence observés dans cette fenêtre',
    'Requests waiting in the bounded queue':
      'Des requêtes attendent dans la file bornée',
    'Number of per-key / per-user buckets currently exceeding their configured cap.':
      'Nombre de bacs par clé / par utilisateur actuellement au-dessus de leur limite configurée.',
    'Sorted by utilisation, hottest first':
      'Trié par utilisation, du plus chaud au moins chaud',
    '{{n}} over cap': '{{n}} hors limite',
    'Live · 3s poll': 'En direct · interrogation 3 s',
    'Auto-refreshing': 'Actualisation automatique',
    Live: 'En direct',
    'Last hour': 'Dernière heure',
    'Last 6 hours': '6 dernières heures',
    'Last 24 hours': '24 dernières heures',
    'Last 3 days': '3 derniers jours',
    'Last 7 days': '7 derniers jours',
    'Custom range': 'Plage personnalisée',
    'No samples in this window.': 'Aucun échantillon dans cette fenêtre.',
    'Pick a start and end time.': 'Choisissez une heure de début et de fin.',
    'Peak in-flight': 'Pic en cours',
    'Average in-flight': 'Moyenne en cours',
    'Active share': 'Part active',
    'Highest in-flight requests in the selected window.':
      'Nombre maximal de requêtes en cours observé dans la fenêtre sélectionnée.',
    'Average in-flight requests across the selected window.':
      'Nombre moyen de requêtes en cours sur la fenêtre sélectionnée.',
    'Share of sampled time with at least one request in flight.':
      'Part du temps échantillonné avec au moins une requête en cours.',
    'Failed to load this window.':
      'Échec du chargement de cette fenêtre (session peut-être expirée).',
    'Buckets in the window': 'Buckets de la fenêtre',
    // Bounded wait-queue settings card + live queued counter (/concurrency).
    'Bounded wait queue': "File d'attente bornée",
    'When a concurrency bucket is full, requests wait in a bounded queue instead of failing immediately. Queue depth = min(depth ratio × concurrency limit, depth cap). A request that waits longer than the max wait, or finds the queue full, receives a 429 response.':
      "Quand un compartiment de concurrence est plein, les requêtes attendent dans une file d'attente bornée au lieu d'échouer immédiatement. Profondeur de file = min(ratio de profondeur × limite de concurrence, plafond de profondeur). Une requête qui attend plus que le délai maximal, ou qui trouve la file pleine, reçoit une réponse 429.",
    'Enable queueing': 'Activer la mise en file',
    'When disabled, requests beyond the concurrency limit are rejected immediately with 429.':
      'Désactivé, les requêtes dépassant la limite de concurrence sont rejetées immédiatement avec un 429.',
    'Depth ratio': 'Ratio de profondeur',
    'Queue depth before capping: this multiple of the concurrency limit. Allowed values: 0 to 64.':
      'Profondeur de file avant plafonnement : ce multiple de la limite de concurrence. Valeurs admises : 0 à 64.',
    'Depth cap': 'Plafond de profondeur',
    'Upper bound on queued requests per bucket. Allowed values: 1 to 1024.':
      'Plafond des requêtes en file par compartiment. Valeurs admises : 1 à 1024.',
    'Max wait': 'Attente maximale',
    'How long a request may wait before it is rejected with 429. Allowed values: 0 to 60.':
      "Durée d'attente maximale d'une requête avant d'être rejetée avec un 429. Valeurs admises : 0 à 60.",
    'Allowed values: 0 to 64': 'Valeurs admises : 0 à 64',
    'Allowed values: 1 to 1024': 'Valeurs admises : 1 à 1024',
    'Allowed values: 0 to 60': 'Valeurs admises : 0 à 60',
    'Enter a whole number': 'Saisissez un nombre entier',
    'Save queue settings': 'Enregistrer les paramètres de file',
  },
  ja: {
    // One-screen /concurrency refactor (Variant E): header, verdict band,
    // scope-table over-cap badge + sort hint, EWMA live badge.
    'In-flight request counts per key, per user, and per channel. Refreshes every 3 seconds.':
      'キー別、ユーザー別、チャネル別の実行中リクエスト数。3 秒ごとに更新されます。',
    'KPI, trend and buckets follow this window':
      'KPI・トレンド・バケットはすべてこのウィンドウに従います',
    'Action needed — {{n}} buckets over cap':
      '対応が必要 — {{n}} 個のバケットが上限超過',
    'All buckets within caps': 'すべてのバケットが上限内です',
    'Backend degraded': 'バックエンド劣化',
    'Backend healthy': 'バックエンド正常',
    'Over-limit {{n}}': '上限超過 {{n}}',
    'Rejected {{n}}': '拒否 {{n}}',
    'Queued {{n}}': '待機中 {{n}}',
    'Rejections observed in this window': 'このウィンドウで並列拒否を検出',
    'Requests waiting in the bounded queue': 'リクエストが有界キューで待機中',
    'Number of per-key / per-user buckets currently exceeding their configured cap.':
      '設定された上限を超えるキー別 / ユーザー別バケットの数。',
    'Sorted by utilisation, hottest first': '利用率の高い順に並べ替え',
    '{{n}} over cap': '{{n}} 件超過',
    'Live · 3s poll': 'ライブ · 3 秒ポーリング',
    'Auto-refreshing': '自動更新',
    Live: 'リアルタイム',
    'Last hour': '過去1時間',
    'Last 6 hours': '過去6時間',
    'Last 24 hours': '過去24時間',
    'Last 3 days': '過去3日',
    'Last 7 days': '過去7日',
    'Custom range': 'カスタム期間',
    'No samples in this window.': 'この期間のサンプルはありません。',
    'Pick a start and end time.': '開始と終了の時刻を選択してください。',
    'Peak in-flight': '処理中ピーク',
    'Average in-flight': '処理中平均',
    'Active share': '稼働率',
    'Highest in-flight requests in the selected window.':
      '選択した期間に観測された最大の処理中リクエスト数。',
    'Average in-flight requests across the selected window.':
      '選択した期間の平均処理中リクエスト数。',
    'Share of sampled time with at least one request in flight.':
      '少なくとも1件のリクエストが処理中だったサンプル時間の割合。',
    'Failed to load this window.':
      'この期間の読み込みに失敗しました（ログインの有効期限切れの可能性）。',
    'Buckets in the window': '期間内のバケット',
    // Bounded wait-queue settings card + live queued counter (/concurrency).
    'Bounded wait queue': '制限付き待機キュー',
    'When a concurrency bucket is full, requests wait in a bounded queue instead of failing immediately. Queue depth = min(depth ratio × concurrency limit, depth cap). A request that waits longer than the max wait, or finds the queue full, receives a 429 response.':
      '並行バケットが満杯になると、リクエストは即座に失敗せず、制限付き待機キューで待機します。キューの深さ = min(深さ倍数 × 並行数上限, 深さ上限)。最長待機時間を超えたリクエスト、またはキューが満杯のリクエストには 429 応答が返ります。',
    'Enable queueing': '待機キューを有効にする',
    'When disabled, requests beyond the concurrency limit are rejected immediately with 429.':
      '無効にすると、並行数上限を超えるリクエストは即座に 429 で拒否されます。',
    'Depth ratio': '深さ倍数',
    'Queue depth before capping: this multiple of the concurrency limit. Allowed values: 0 to 64.':
      '上限適用前のキューの深さ:並行数上限のこの倍数。許可値:0 から 64。',
    'Depth cap': '深さ上限',
    'Upper bound on queued requests per bucket. Allowed values: 1 to 1024.':
      'バケットごとのキュー内リクエスト数の上限。許可値:1 から 1024。',
    'Max wait': '最大待機時間',
    'How long a request may wait before it is rejected with 429. Allowed values: 0 to 60.':
      'リクエストが 429 で拒否されるまで待機できる時間。許可値:0 から 60。',
    'Allowed values: 0 to 64': '許可値:0 から 64',
    'Allowed values: 1 to 1024': '許可値:1 から 1024',
    'Allowed values: 0 to 60': '許可値:0 から 60',
    'Enter a whole number': '整数を入力してください',
    'Save queue settings': '待機キュー設定を保存',
  },
  ru: {
    // One-screen /concurrency refactor (Variant E): header, verdict band,
    // scope-table over-cap badge + sort hint, EWMA live badge.
    'In-flight request counts per key, per user, and per channel. Refreshes every 3 seconds.':
      'Счётчики выполняющихся запросов по ключам, пользователям и каналам. Обновление каждые 3 секунды.',
    'KPI, trend and buckets follow this window':
      'KPI, график и бакеты следуют этому окну',
    'Action needed — {{n}} buckets over cap':
      'Требуется действие — {{n}} бакетов выше лимита',
    'All buckets within caps': 'Все бакеты в пределах лимитов',
    'Backend degraded': 'Бэкенд деградирован',
    'Backend healthy': 'Бэкенд в норме',
    'Over-limit {{n}}': 'Превышено {{n}}',
    'Rejected {{n}}': 'Отклонено {{n}}',
    'Queued {{n}}': 'В очереди {{n}}',
    'Rejections observed in this window':
      'Зафиксированы отказы параллелизма в этом окне',
    'Requests waiting in the bounded queue':
      'Запросы ждут в ограниченной очереди',
    'Number of per-key / per-user buckets currently exceeding their configured cap.':
      'Число бакетов по ключам / пользователям, превышающих настроенный лимит.',
    'Sorted by utilisation, hottest first':
      'Отсортировано по загрузке, сначала самые горячие',
    '{{n}} over cap': '{{n}} превышений',
    'Live · 3s poll': 'В реальном времени · опрос 3 с',
    'Auto-refreshing': 'Автообновление',
    Live: 'Реальное время',
    'Last hour': 'Последний час',
    'Last 6 hours': 'Последние 6 часов',
    'Last 24 hours': 'Последние 24 часа',
    'Last 3 days': 'Последние 3 дня',
    'Last 7 days': 'Последние 7 дней',
    'Custom range': 'Пользовательский диапазон',
    'No samples in this window.': 'Нет данных за этот период.',
    'Pick a start and end time.': 'Выберите время начала и окончания.',
    'Peak in-flight': 'Пик запросов',
    'Average in-flight': 'Среднее запросов',
    'Active share': 'Доля активности',
    'Highest in-flight requests in the selected window.':
      'Максимум выполняющихся запросов в выбранном окне.',
    'Average in-flight requests across the selected window.':
      'Среднее число выполняющихся запросов за окно.',
    'Share of sampled time with at least one request in flight.':
      'Доля времени сэмплирования хотя бы с одним запросом.',
    'Failed to load this window.':
      'Не удалось загрузить этот период (возможно, сессия истекла).',
    'Buckets in the window': 'Бакеты за период',
    // Bounded wait-queue settings card + live queued counter (/concurrency).
    'Bounded wait queue': 'Ограниченная очередь ожидания',
    'When a concurrency bucket is full, requests wait in a bounded queue instead of failing immediately. Queue depth = min(depth ratio × concurrency limit, depth cap). A request that waits longer than the max wait, or finds the queue full, receives a 429 response.':
      'Когда лимит параллельных запросов исчерпан, запросы ждут в ограниченной очереди, а не сразу отклоняются. Глубина очереди = min(множитель глубины × лимит параллелизма, потолок глубины). Запрос, превысивший максимальное ожидание или пришедший в полную очередь, получает ответ 429.',
    'Enable queueing': 'Включить очередь',
    'When disabled, requests beyond the concurrency limit are rejected immediately with 429.':
      'Если отключено, запросы сверх лимита параллелизма сразу отклоняются с кодом 429.',
    'Depth ratio': 'Множитель глубины',
    'Queue depth before capping: this multiple of the concurrency limit. Allowed values: 0 to 64.':
      'Глубина очереди до ограничения: этот множитель лимита параллелизма. Допустимые значения: от 0 до 64.',
    'Depth cap': 'Потолок глубины',
    'Upper bound on queued requests per bucket. Allowed values: 1 to 1024.':
      'Верхняя граница запросов в очереди для каждого бакета. Допустимые значения: от 1 до 1024.',
    'Max wait': 'Максимальное ожидание',
    'How long a request may wait before it is rejected with 429. Allowed values: 0 to 60.':
      'Сколько времени запрос может ждать, прежде чем получить 429. Допустимые значения: от 0 до 60.',
    'Allowed values: 0 to 64': 'Допустимые значения: от 0 до 64',
    'Allowed values: 1 to 1024': 'Допустимые значения: от 1 до 1024',
    'Allowed values: 0 to 60': 'Допустимые значения: от 0 до 60',
    'Enter a whole number': 'Введите целое число',
    'Save queue settings': 'Сохранить настройки очереди',
  },
  vi: {
    // One-screen /concurrency refactor (Variant E): header, verdict band,
    // scope-table over-cap badge + sort hint, EWMA live badge.
    'In-flight request counts per key, per user, and per channel. Refreshes every 3 seconds.':
      'Số yêu cầu đang xử lý theo từng khóa, người dùng và kênh. Làm mới mỗi 3 giây.',
    'KPI, trend and buckets follow this window':
      'KPI, xu hướng và nhóm bucket đều theo khung thời gian này',
    'Action needed — {{n}} buckets over cap':
      'Cần xử lý — {{n}} bucket vượt hạn mức',
    'All buckets within caps': 'Tất cả bucket trong hạn mức',
    'Backend degraded': 'Backend suy giảm',
    'Backend healthy': 'Backend bình thường',
    'Over-limit {{n}}': 'Vượt hạn mức {{n}}',
    'Rejected {{n}}': 'Bị từ chối {{n}}',
    'Queued {{n}}': 'Đang xếp hàng {{n}}',
    'Rejections observed in this window':
      'Có yêu cầu bị từ chối do đồng thời trong khung này',
    'Requests waiting in the bounded queue':
      'Yêu cầu đang chờ trong hàng đợi có giới hạn',
    'Number of per-key / per-user buckets currently exceeding their configured cap.':
      'Số bucket theo khóa / theo người dùng hiện vượt quá hạn mức đã cấu hình.',
    'Sorted by utilisation, hottest first':
      'Sắp theo mức sử dụng, nóng nhất trước',
    '{{n}} over cap': '{{n}} vượt hạn mức',
    'Live · 3s poll': 'Trực tiếp · 3 giây/lần',
    'Auto-refreshing': 'Tự động làm mới',
    Live: 'Thời gian thực',
    'Last hour': '1 giờ qua',
    'Last 6 hours': '6 giờ qua',
    'Last 24 hours': '24 giờ qua',
    'Last 3 days': '3 ngày qua',
    'Last 7 days': '7 ngày qua',
    'Custom range': 'Tùy chỉnh khoảng',
    'No samples in this window.': 'Không có mẫu trong khoảng này.',
    'Pick a start and end time.': 'Chọn giờ bắt đầu và kết thúc.',
    'Peak in-flight': 'Đỉnh xử lý',
    'Average in-flight': 'Trung bình xử lý',
    'Active share': 'Tỷ lệ hoạt động',
    'Highest in-flight requests in the selected window.':
      'Số yêu cầu đang xử lý cao nhất trong cửa sổ đã chọn.',
    'Average in-flight requests across the selected window.':
      'Số yêu cầu đang xử lý trung bình trong cửa sổ đã chọn.',
    'Share of sampled time with at least one request in flight.':
      'Tỷ lệ thời gian mẫu có ít nhất một yêu cầu đang xử lý.',
    'Failed to load this window.':
      'Không tải được khoảng này (phiên đăng nhập có thể đã hết hạn).',
    'Buckets in the window': 'Các bucket trong khoảng',
    // Bounded wait-queue settings card + live queued counter (/concurrency).
    'Bounded wait queue': 'Hàng đợi chờ có giới hạn',
    'When a concurrency bucket is full, requests wait in a bounded queue instead of failing immediately. Queue depth = min(depth ratio × concurrency limit, depth cap). A request that waits longer than the max wait, or finds the queue full, receives a 429 response.':
      'Khi bucket đồng thời đã đầy, yêu cầu sẽ chờ trong hàng đợi chờ có giới hạn thay vì thất bại ngay. Chiều sâu hàng đợi = min(tỷ lệ chiều sâu × giới hạn đồng thời, trần chiều sâu). Yêu cầu chờ lâu hơn thời gian chờ tối đa, hoặc đến khi hàng đợi đầy, sẽ nhận phản hồi 429.',
    'Enable queueing': 'Bật xếp hàng',
    'When disabled, requests beyond the concurrency limit are rejected immediately with 429.':
      'Khi tắt, yêu cầu vượt giới hạn đồng thời sẽ bị từ chối ngay với 429.',
    'Depth ratio': 'Tỷ lệ chiều sâu',
    'Queue depth before capping: this multiple of the concurrency limit. Allowed values: 0 to 64.':
      'Chiều sâu hàng đợi trước khi cắt: bội số này của giới hạn đồng thời. Giá trị cho phép: 0 đến 64.',
    'Depth cap': 'Trần chiều sâu',
    'Upper bound on queued requests per bucket. Allowed values: 1 to 1024.':
      'Giới hạn trên số yêu cầu chờ trong mỗi bucket. Giá trị cho phép: 1 đến 1024.',
    'Max wait': 'Thời gian chờ tối đa',
    'How long a request may wait before it is rejected with 429. Allowed values: 0 to 60.':
      'Thời gian yêu cầu có thể chờ trước khi bị từ chối với 429. Giá trị cho phép: 0 đến 60.',
    'Allowed values: 0 to 64': 'Giá trị cho phép: 0 đến 64',
    'Allowed values: 1 to 1024': 'Giá trị cho phép: 1 đến 1024',
    'Allowed values: 0 to 60': 'Giá trị cho phép: 0 đến 60',
    'Enter a whole number': 'Nhập một số nguyên',
    'Save queue settings': 'Lưu cài đặt xếp hàng',
  },
}

async function main() {
  let totalAdded = 0
  let totalRemoved = 0

  for (const [locale, trans] of Object.entries(newKeys)) {
    const filePath = path.join(LOCALES_DIR, `${locale}.json`)
    const json = JSON.parse(await fs.readFile(filePath, 'utf8'))

    let count = 0
    for (const [key, value] of Object.entries(trans)) {
      if (json.translation[key] !== value) {
        json.translation[key] = value
        count++
      }
    }
    for (const retired of RETIRED_KEYS) {
      if (retired in json.translation) {
        delete json.translation[retired]
        count++
        totalRemoved++
      }
    }

    // Keep the flat translation object alphabetically sorted like sync does.
    json.translation = Object.fromEntries(
      Object.entries(json.translation).sort(([a], [b]) => {
        if (a === b) return 0
        return a < b ? -1 : 1
      })
    )

    if (count > 0) {
      await fs.writeFile(filePath, stableStringify(json), 'utf8')
      totalAdded += count
      console.log(`${locale}: ${count} change(s) written`)
    } else {
      console.log(`${locale}: up to date`)
    }
  }

  console.log(
    `done: ${totalAdded} write(s), ${totalRemoved} retired key(s) removed`
  )
}

main().catch((err) => {
  console.error(err)
  process.exit(1)
})
