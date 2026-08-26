# Changelog

## 1.0.7

- В редактор профиля добавлена необязательная опция «Сбросить распределение
  клиентов». При публикации назначенные этому профилю ноды один раз очищают
  только принадлежащие EzhikLB IPVS-состояния и связанные conntrack-записи,
  после чего создают правила заново: следующие пакеты распределяются между
  backend-серверами без старой affinity-привязки.
- Опция по умолчанию выключена и содержит предупреждение о прерывании активных
  TCP/UDP-сессий. Ноды со старыми агентами необходимо сначала обновить до
  `1.0.7`; панель и API не позволят запустить неподдерживаемый сброс.
- Сброс не использует глобальные `ipvsadm -C` или `conntrack -F` и не затрагивает
  чужие службы, таблицы и соединения на VPS.

## 1.0.6

- Исправлено дёрганое перетаскивание записей маршрутизации (добавлено в
  1.0.4): позиция строки замерялась вместе с уже применённым к ней смещением
  (у перетаскиваемой строки оно есть всегда, у соседней — могло не успеть
  доиграть предыдущую анимацию), из-за чего смещение накапливалось кадр за
  кадром и перетаскиваемая запись «прыгала» при пересечении с соседней.
  Теперь перед каждым замером позиции transform явно сбрасывается.

## 1.0.5

- Добавлено принудительное удаление ноды, зависшей в состоянии «Удаление…»
  дольше минуты без ответа (например, если её агент был заменён новой
  установкой на тот же VPS и уже никогда не подтвердит очистку). Кнопка
  «Удалить принудительно» появляется в строке зависшей ноды только по
  истечении этого времени и требует отдельного подтверждения с явным
  предупреждением, что IPVS/firewall-правила на самой VPS при этом не
  очищаются автоматически.
- Обычное удаление (`Удалить ноду`) не изменилось: панель по-прежнему ждёт
  подтверждения от агента, прежде чем убрать запись.

## 1.0.4

- Добавлено перетаскивание записей маршрутизации мышкой (или тапом на сенсорном
  экране) для изменения их порядка в списке — за отдельную ручку слева от
  переключателя. Порядок влияет только на отображение и сохраняется вместе с
  профилем, но не влияет на распределение трафика.
- Перетаскивание анимировано: остальные записи плавно сдвигаются на освободившееся
  место (FLIP-анимация), сама перетаскиваемая запись слегка увеличивается и
  получает тень, а после отпускания плавно занимает финальную позицию. Анимация
  отключается при `prefers-reduced-motion`.

## 1.0.3

- Исправлен пустой экран панели сразу после входа для роли «Панель» без
  подключённых нод: `/api/v1/nodes` и `/api/v1/profiles` возвращали `null`
  вместо `[]`, когда список был пуст (`nil`-срез Go сериализуется в JSON как
  `null`), а фронтенд падал на `nodes.filter(...)`. Раньше это не проявлялось,
  потому что нода `local` создавалась автоматически и список никогда не был
  пуст; после фикса 1.0.2 (удаление фиктивной локальной ноды для роли
  «Панель») список нод стал легитимно пустым, и баг стал воспроизводимым.
- Backend: `ListNodes`/`ListProfiles` теперь всегда возвращают пустой массив,
  а не `nil`.
- Frontend: все списковые ответы API (`nodes`, `profiles`, `revisions`,
  `health`, `stats`, `metricHistory`, `events`) проходят через общую
  нормализацию и приводятся к `[]`, даже если сервер всё же прислал
  некорректное значение — страница больше не может упасть на этом целиком.

## 1.0.2

- Чистая установка роли «Панель» больше не создаёт фиктивную локальную ноду без агента.
- При обновлении панели ошибочно созданная ранее `Local node` удаляется, при этом подключённые
  удалённые ноды и профили сохраняются.
- Роль «Панель + локальная нода» по-прежнему создаёт и восстанавливает локальную ноду.

## 1.0.1

- При новой установке панели добавлен отдельный выбор портов web-интерфейса и API нод.
- Установщик проверяет диапазон портов, запрещает назначать обоим интерфейсам один порт и
  предлагает другое значение, если выбранный TCP-порт уже занят.
- При обновлении существующей установки сохранённые порты остаются без изменений.

## 1.0.0

- Первый стабильный релиз EzhikLB: TCP/UDP-балансировка, профили, удалённые
  ноды, ICMP health-check, Affinity, наблюдаемость и управляемые обновления.
- Уведомление о результате обновления ноды перенесено в отдельный фиксированный
  слой справа снизу; оно больше не сдвигает таблицу и не обрезается контейнером.
- Добавлены разные доступные состояния успеха/ошибки, ручное закрытие,
  автоматическое скрытие и поддержка `prefers-reduced-motion`.
- README переработан как пользовательская документация: краткое позиционирование,
  сравнение с NGINX/HAProxy, установка, подключение нод и совместная работа с Caddy/Docker.

## 0.1.0-beta.3.5

- Исправлены права systemd для self-update агента: при `ProtectSystem=strict`
  каталог `/opt/ezhiklb/bin` теперь явно доступен на запись только сервису ноды.
- Атомарная замена проверенного бинарника больше не завершается ошибкой
  `open /opt/ezhiklb/bin/.ezhiklb-agent-update-*: read-only file system`.

## 0.1.0-beta.3.4

- Исправлено восстановление ICMP health-check после self-update: устаревший
  `update_target` больше не прерывает синхронизацию активного профиля.
- Настройки health-check теперь дописываются в старый локальный `state.json` без
  повторного применения IPVS и сохраняются для работы при недоступной панели.
- Изменение веса backend ограничено таймаутом, чтобы зависший `ipvsadm` не мог
  навсегда остановить цикл проверок; добавлены диагностические сообщения запуска
  и остановки монитора.

## 0.1.0-beta.3.3

- Исправлена валидация версии self-update: буквы из `beta` больше не считаются
  запрещёнными символами, версия проверяется строгим безопасным шаблоном.
- Удалена dash-анимация SVG, которая визуально разрывала линии ближе к концу графика.
- Tooltip выбирает свободное место выше или ниже фактических точек и больше не
  закрывает маркер в центральной части графика.
- Агент не выполняет downgrade по устаревшему update-target; установленная более
  новая версия закрывает старый запрос как завершённый.

## 0.1.0-beta.3.2

- Старые агенты больше не сбрасывают запрос обновления в `idle`; для версий до
  `beta.3` интерфейс честно показывает необходимость первого ручного перехода.
- Во время активного обновления ноды панель опрашивает состояние каждые 2 секунды,
  а завершённый статус сохраняется до следующего запроса и не теряется между polling-циклами.
- Графики получили запас сверху для пиков, адаптивное позиционирование подсказки
  у левого/правого края, мягкую заливку, живую конечную точку и анимацию построения.

## 0.1.0-beta.3.1

- Added real per-stage progress reporting for node self-update (`downloading`, `verifying`,
  `installing`, `restarting`), each reported to the panel via heartbeat as it starts, and shown
  as an orange progress bar on the node row while an update is in flight.
- Changed the "Обновить" button to hide while an update is already in progress, preventing
  duplicate update requests.
- Changed the node-update notification to fire on confirmed completion or failure (detected from
  the node's own reported state) instead of immediately after the request was merely accepted.
- Fixed a version-comparison bug that made `isOlderVersion` treat versions like `0.1.0-beta.3.1`
  as equal to `0.1.0-beta.3` (it only compared the first number after the prerelease channel,
  dropping any further `.N` segments), which could hide the update button entirely.
- Added animated hover tooltips to the Overview metric charts: a crosshair with per-series
  markers and a floating panel showing the exact time and value under the pointer.

## 0.1.0-beta.3

- Added minute-bucket node metric history with a 24-hour retention window and four Overview
  charts (network, CPU, RAM, active IPs) with a per-node/all-nodes selector.
- Added a node diagnostics check (IPVS reachability, EzhikLB firewall chain readiness, service
  and destination counts) shown on every node's detail dialog.
- Added a signed one-click node self-update: the panel only ever hands the agent a target
  version, the agent downloads the matching official release archive, verifies its SHA-256
  checksum and atomically replaces its own binary before restarting.
- Replaced the "online" text badge next to each node with status squares: pulsing green
  (online), blinking orange (applying), pulsing red (offline/unreachable), static dark
  (disabled).
- Replaced the remaining browser `confirm()` prompts (closing an unsaved profile or listener,
  restoring a profile revision, deleting a routing entry) with the panel's own confirmation
  dialogs.
- Translated the Health page's reachability states and latency label into Russian.
- Improved the event journal: added labels for token rotation/revocation, manual health probes
  and update requests, and resolved node/profile IDs to their current names when the audit
  record itself has no name.
- Added a non-blocking warning when a listener's backend address matches one of the panel's
  known node IPs.
- Aligned the local node's two action buttons to the same optical positions a third button would
  use, matching the row width of remote nodes.

## 0.1.0-beta.2

- Added autonomous restoration of the last successfully applied IPVS and firewall state when a node boots while the panel is unavailable.
- Added automatic `v1`, `v2`, `v3` profile versions and validated custom versions with immutable publication history.
- Replaced internal revision counters in the node interface with clear apply status and semantic profile version labels.
- Added an event journal with node, profile and error filters and automatic 14-day retention.
- Added the project GitHub link to the sidebar and aligned compact load metrics with the rest of each node row.

## 0.1.0-beta.1

- Added lightweight one-minute node telemetry for RAM, CPU, load average, network throughput and unique active client IPs.
- Added compact live node cards with resource meters, traffic rates, connection uptime and an accessible online pulse.
- Changed remote-node deletion into an acknowledged decommission flow that removes managed IPVS/firewall state and disables the agent before the panel forgets the node.
- Added a persistent `deleting` state for offline nodes so cleanup resumes when they reconnect.
- Allowed ordinary upgrades of already enrolled nodes while the panel is temporarily unavailable; strict first-revision verification remains enabled for new and replacement enrollment.
- Changed the agent unit to restart only on failure so a completed decommission can stop it cleanly.

## 0.1.0-alpha.8.2

- Fixed reinstalling or re-enrolling an existing node VPS so the supplied panel URL, node ID and credential replace stale values in the persisted environment.
- Fixed ordinary node upgrades so saved enrollment values are reused without asking for the ID and credential again.
- Increased the installation-command viewport and separated its text from the horizontal scrollbar.
- Replaced the enrollment radar with a clear animated panel-to-node connection path and simplified the waiting copy.

## 0.1.0-alpha.8

- Split the administrator panel and node-agent API onto configurable ports, defaulting to `8080` and `8081`.
- Added safe in-panel port changes with automatic service restart, browser redirect and bounded previous panel/API listeners for migration.
- Rebuilt node enrollment around a single-line scrollable command, copy action and live connecting/success animation.
- Added automatically observed node IPv4 addresses, continuous online uptime, last heartbeat and apply-stage reporting.
- Simplified node actions to edit, enable/disable and delete while retaining credentials during temporary disablement.
- Added node detail dialogs with profile, revision, health, error and live IPVS route information.
- Grouped dashboard traffic by expandable node sections.

## 0.1.0-alpha.7.3

- Centered the animated checkmark precisely inside every square state control.
- Replaced manual backend action offsets with stable vertical grid alignment.

## 0.1.0-alpha.7.2

- Replaced unstable slider switches with accessible square checkboxes and animated SVG checkmarks across listeners, backends and health checks.

## 0.1.0-alpha.7.1

- Fixed all dialogs being positioned relative to the animated page container, which cropped node and listener editors.
- Print the detected server IPv4 after network-access panel installation instead of `0.0.0.0` and a placeholder.

## 0.1.0-alpha.7

- Simplified the public README to one interactive installation command and a focused two-VPS test flow.
- Added interactive panel access and node enrollment prompts to the installer.
- Reduced new-node creation to a name-first two-step flow with automatic initial profile assignment.
- Hardened the generated one-line node command with dependency installation and SHA-256 verification.
- Added automatic `EZHIKLB_ALLOW_INSECURE=1` enrollment when the selected panel URL uses HTTP.
- Marked alpha tags as GitHub pre-releases automatically.
- Replaced the oversized Affinity panel with a compact custom dropdown and optional custom-seconds field.
- Replaced native profile/scheduler selects with the shared EzhikLB dropdown component.
- Replaced browser prompts for node editing and profile cloning with proper panel dialogs.
- Fixed backend switch alignment and constrained dialogs to the viewport without horizontal overflow.
- Increased small labels, metadata, helpers, table values and navigation text throughout the panel.

## 0.1.0-alpha.6

- Fixed the rule-editor field grid and increased small text throughout the panel.
- Replaced the raw affinity number with documented presets: off, 15/30 minutes, 1/3/5/24 hours, and a custom seconds value.
- Added guidance for stateful UDP/VPN traffic and backend failover behavior.
- Added profile cloning, guarded deletion, revision history and rollback-as-a-new-revision.
- Added remote-node creation with a unique credential shown once, generated install command, credential rotation, rename and removal.
- Added remote-node disable/re-enable and on-demand ICMP probes executed by each node agent.
- Kept reusable profile assignment for both local and remote nodes and expanded node version/last-seen/apply status.
- Remote agents now reject public plain HTTP unless the test-only `EZHIKLB_ALLOW_INSECURE=1` switch is explicit.
- Added an alpha.6 multi-node, TCP/UDP, weighted distribution and health-failover test plan.

## 0.1.0-alpha.5

- Replaced expanded listener cards with compact Cloudflare-style rule rows.
- Added focused rule editing, cloning, delete confirmation and unsaved-change protection.
- Added client/server validation for conflicting listeners and duplicate backends.
- Added calculated backend weight percentages and inline ICMP health state.
- Added persisted per-service and per-backend IPVS counters to the Overview page.
- Added explicit applying/applied/error node states.
- Reduced heartbeat write frequency to 15 seconds and added revision ETags.
- Improved upgrade backups and rollback data restoration.
- Added safe cache headers so panel upgrades load the current frontend.

## 0.1.0-alpha.4

- Fixed listener creation on panels opened over plain HTTP.

## 0.1.0-alpha.3

- Fixed executable directory permissions and installer version detection.
