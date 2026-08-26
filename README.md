# EzhikLB

> Лёгкая web-панель для управления TCP- и UDP-балансировкой на Linux.

**EzhikLB (Ezhik Load Balancer)** объединяет панель управления, переиспользуемые профили и удалённые ноды. Трафик обрабатывается IPVS непосредственно в ядре Linux, а панель отвечает за конфигурацию, health-check, наблюдение и обновления.

![Version](https://img.shields.io/badge/version-1.0.1-65c795?style=flat-square)
![Protocols](https://img.shields.io/badge/protocols-TCP%20%2B%20UDP-e7e3dc?style=flat-square)
![Platform](https://img.shields.io/badge/platform-Linux-9fa6b2?style=flat-square)

## Главное

- TCP, UDP или оба протокола в одной записи;
- балансировка по весам и планировщики `wrr` / `rr`;
- Affinity для закрепления IP клиента за backend;
- ICMP health-check с автоматическим исключением недоступных адресов;
- одна панель и несколько удалённых нод;
- общие профили, версии, история изменений и откат;
- графики RAM, CPU, сети и активных IP;
- состояние IPVS, firewall и применения конфигурации;
- обновление подключённых нод одной кнопкой с проверкой SHA-256;
- сохранение последней конфигурации на ноде: трафик продолжает работать при недоступной панели.

## Сравнение

Таблица сравнивает EzhikLB с базовыми open-source установками без сторонних панелей и коммерческих модулей.

| Возможность | EzhikLB 1.0 | NGINX Open Source | HAProxy Community |
|---|:---:|:---:|:---:|
| Балансировка TCP | Да | Да, модуль `stream` | Да |
| Универсальная балансировка UDP | Да | Да, модуль `stream` | Нет, UDP-модуль относится к HAProxy Enterprise |
| TCP и UDP в одной записи | Да | Настраиваются отдельно | Не в Community |
| Веса backend-серверов | Да | Да | Да |
| Активная проверка доступности без платной редакции | Да, ICMP | Только пассивная; периодическая активная проверка относится к NGINX Plus | Да для поддерживаемых режимов |
| Полноценное управление конфигурацией из web | Да | Нет | Нет; встроенная страница предназначена в основном для статистики |
| Централизованное управление несколькими нодами | Да | Нет | Нет |
| Общие профили и назначение на ноды | Да | Нет | Нет |
| История версий и откат профиля | Да | Нет | Нет |
| Обновление ноды одной кнопкой | Да | Нет | Нет |
| Работа data plane без панели | Да | Да | Да |

NGINX умеет универсально проксировать TCP/UDP через `stream`, но периодические активные health-check и динамическая конфигурация upstream описаны как возможности коммерческой подписки. HAProxy Community отлично подходит для TCP/HTTP и имеет страницу статистики, однако универсальная UDP-балансировка поставляется отдельным модулем HAProxy Enterprise. Источники: [NGINX stream](https://nginx.org/en/docs/stream/ngx_stream_upstream_module.html), [NGINX active health checks](https://nginx.org/en/docs/stream/ngx_stream_upstream_hc_module.html), [HAProxy UDP module](https://www.haproxy.com/documentation/haproxy-enterprise/enterprise-modules/udp-load-balancing/overview/), [HAProxy Stats page](https://www.haproxy.com/blog/exploring-the-haproxy-stats-page).

EzhikLB ориентирован именно на простое управление L4-балансировкой. Он не заменяет возможности Caddy, NGINX или HAProxy на уровне HTTP: TLS termination, WAF, маршрутизацию по доменам и заголовкам лучше оставлять специализированному reverse proxy.

## Как устроено

```text
Браузер → Панель EzhikLB → API нод → Агент → IPVS → Backend-серверы
```

- **Панель** хранит профили, версии, ноды, события и телеметрию.
- **Агент** применяет назначенный профиль и отправляет состояние ноды.
- **IPVS** обрабатывает пользовательский TCP/UDP-трафик в ядре Linux.

Панель не находится в пути пользовательского трафика. Если она временно выключена, уже применённые правила и health-check продолжают работать на нодах.

## Требования

- Debian или Ubuntu `amd64`;
- root или `sudo`;
- доступ к GitHub Releases во время установки и обновления;
- открытые TCP/UDP-порты выбранных маршрутов;
- для удалённых нод — доступ к API панели, по умолчанию TCP `8081`.

## Установка

Одна команда запускает интерактивный установщик. Внутри можно выбрать панель, ноду или оба компонента:

```bash
sudo apt-get update && sudo apt-get install -y ca-certificates curl && ezhik_version=1.0.1 && ezhik_tmp=$(mktemp -d) && cd "$ezhik_tmp" && curl -fLO "https://github.com/ezhikdev/ezhiklb/releases/download/v${ezhik_version}/ezhiklb_${ezhik_version}_linux_amd64.tar.gz" && curl -fLO "https://github.com/ezhikdev/ezhiklb/releases/download/v${ezhik_version}/ezhiklb_${ezhik_version}_linux_amd64.tar.gz.sha256" && sha256sum -c "ezhiklb_${ezhik_version}_linux_amd64.tar.gz.sha256" && tar -xzf "ezhiklb_${ezhik_version}_linux_amd64.tar.gz" && sudo ./install.sh && cd / && rm -rf -- "$ezhik_tmp"
```

Варианты установки:

1. **Панель** — web-интерфейс и API управления нодами.
2. **Нода** — только агент и IPVS data plane.
3. **Панель + локальная нода** — управление и балансировка на одном VPS.

При новой установке панели скрипт отдельно спросит порт web-интерфейса (`8080` по умолчанию)
и порт API нод (`8081` по умолчанию). Порты должны различаться; если выбранный порт уже занят,
установщик сообщит об этом и предложит ввести другой. При обновлении действующие порты сохраняются.

Повторный запуск установщика обновляет компоненты, сохраняя базу и конфигурацию. Перед обновлением автоматически создаётся резервная копия в `/var/backups/ezhiklb`.

## Первый вход

По умолчанию web-панель использует порт `8080`, а API нод — `8081`.

Получить токен администратора:

```bash
sudo sed -n 's/^EZHIKLB_ADMIN_TOKEN=//p' /etc/ezhiklb/ezhiklb.env
```

Если панель доступна только локально, создайте SSH-туннель со своего компьютера:

```bash
ssh -L 8080:127.0.0.1:8080 root@IP_ПАНЕЛИ
```

После этого откройте <http://127.0.0.1:8080>.

## Подключение ноды

1. Откройте **Ноды → Добавить ноду**.
2. Укажите название, например `server-1`.
3. Проверьте публичный адрес API панели.
4. Скопируйте сгенерированную команду и выполните её на VPS ноды.

Панель автоматически покажет подключение, IPv4, uptime, текущую нагрузку и состояние применения профиля. Один профиль можно назначить нескольким нодам.

## Создание маршрута

1. Откройте профиль и добавьте запись.
2. Укажите входной адрес и порт.
3. Выберите TCP, UDP или оба протокола.
4. Добавьте backend-серверы и веса.
5. При необходимости включите Affinity и health-check.
6. Сохраните запись и опубликуйте новую версию профиля.

Веса `1 + 1` дают примерно `50% / 50%`, а `2 + 1` — примерно `66% / 33%`. Для VPN и долгоживущих UDP-сессий разумная начальная настройка Affinity — **3 часа**.

ICMP health-check проверяет доступность IP-адреса, но не подтверждает работу конкретного приложения на TCP/UDP-порту.

## Caddy, Docker и существующие сайты

Панель можно установить рядом с Caddy и Docker. Самый бесконфликтный вариант — выбрать роль **Панель** без локальной ноды:

- Caddy продолжает занимать `80/443`;
- EzhikLB слушает локальный `8080` и API нод `8081`;
- panel-only не создаёт IPVS-маршруты и не изменяет firewall-цепочки Docker;
- Caddy может завершать HTTPS и проксировать запросы к панели.

Если Caddy работает непосредственно на хосте или использует `network_mode: host`:

```caddyfile
lb.example.com {
    reverse_proxy 127.0.0.1:8080
}

nodes-lb.example.com {
    reverse_proxy 127.0.0.1:8081
}
```

Если Caddy находится в обычной Docker bridge-сети, `127.0.0.1` указывает на сам
контейнер. Добавьте контейнеру доступ к шлюзу хоста:

```yaml
services:
  caddy:
    extra_hosts:
      - "host.docker.internal:host-gateway"
```

И используйте его в Caddyfile:

```caddyfile
lb.example.com {
    reverse_proxy host.docker.internal:8080
}

nodes-lb.example.com {
    reverse_proxy host.docker.internal:8081
}
```

В этом варианте EzhikLB должен слушать адрес, доступный с Docker bridge, а
прямой внешний доступ к `8080/8081` лучше закрыть firewall.

Справка: [Caddy `reverse_proxy`](https://caddyserver.com/docs/caddyfile/directives/reverse_proxy), [Docker `host-gateway`](https://docs.docker.com/compose/how-tos/networking/#custom-hosts).

При добавлении ноды в панели укажите `https://nodes-lb.example.com` как адрес API.

Режим **Панель + локальная нода** тоже возможен, но нельзя создавать маршруты на портах, уже занятых Caddy или Docker. Для критичных сайтов безопаснее держать control plane EzhikLB на отдельном небольшом VPS.

## Обновление

Панель обновляется повторным запуском команды установки. Подключённые агенты после этого можно обновить одной кнопкой в разделе **Ноды**. Агент:

1. скачивает официальный архив релиза;
2. проверяет SHA-256;
3. атомарно заменяет бинарник;
4. перезапускается через systemd;
5. сообщает панели реальный прогресс и результат.

## Полезные команды

```bash
# Версия
sudo cat /etc/ezhiklb/version

# Состояние служб
sudo systemctl status ezhiklb ezhiklb-agent --no-pager -l

# Последние журналы
sudo journalctl -u ezhiklb -u ezhiklb-agent -n 100 --no-pager -l

# Проверка панели и API нод
curl -fsS http://127.0.0.1:8080/healthz && echo
curl -fsS http://127.0.0.1:8081/healthz && echo

# Текущие IPVS-правила и статистика
sudo ipvsadm -Ln
sudo ipvsadm -Ln --stats

# Сохранённое состояние агента
sudo cat /var/lib/ezhiklb-agent/state.json

# Перезапуск
sudo systemctl restart ezhiklb ezhiklb-agent
```

## Безопасность

- Для публичного доступа закройте панель HTTPS через Caddy или другой reverse proxy.
- Между удалёнными нодами и панелью также рекомендуется HTTPS.
- Обычный HTTP допустим только в доверенной или тестовой сети и требует явного разрешения.
- Не публикуйте административный токен и токены нод.
- Регулярно сохраняйте `/etc/ezhiklb`, `/var/lib/ezhiklb` и `/var/lib/ezhiklb-agent`.

## Документация

- [Архитектура](docs/ARCHITECTURE.md)
- [План развития](docs/ROADMAP.md)
- [Сценарии тестирования](docs/TESTING.md)
