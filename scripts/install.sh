#!/usr/bin/env bash
set -Eeuo pipefail

EZHIKLB_VERSION="1.0.2"
PREFIX="/opt/ezhiklb"
CONFIG_DIR="/etc/ezhiklb"
DATA_DIR="/var/lib/ezhiklb"
AGENT_DATA_DIR="/var/lib/ezhiklb-agent"
WEB_DIR="/usr/share/ezhiklb/web"
ENV_FILE="${CONFIG_DIR}/ezhiklb.env"
VERSION_FILE="${CONFIG_DIR}/version"
SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
if [[ -d "${SCRIPT_DIR}/bin" ]]; then
  BUNDLE_DIR="$SCRIPT_DIR"
else
  BUNDLE_DIR="$(cd -- "${SCRIPT_DIR}/.." && pwd)"
fi
BACKUP_ROOT="/var/backups/ezhiklb"
backup_dir=""

log() { printf '\n\033[1;36mEzhikLB\033[0m %s\n' "$*"; }
die() { printf '\nEzhikLB installer error: %s\n' "$*" >&2; exit 1; }

detect_server_ipv4() {
  local detected=""
  if command -v ip >/dev/null 2>&1; then
    detected="$(ip -4 route get 1.1.1.1 2>/dev/null | awk '{for(i=1;i<=NF;i++) if($i=="src"){print $(i+1);exit}}')"
  fi
  if [[ -z "$detected" ]] && command -v hostname >/dev/null 2>&1; then
    detected="$(hostname -I 2>/dev/null | awk '{for(i=1;i<=NF;i++) if($i ~ /^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$/ && $i !~ /^127\./){print $i;exit}}')"
  fi
  printf '%s' "$detected"
}

restore_previous_release() {
  [[ -n "$backup_dir" ]] || return 0
  log "Restoring previous EzhikLB release"
  systemctl stop ezhiklb-agent.service ezhiklb.service 2>/dev/null || true
  [[ -d "${backup_dir}/bin" ]] && cp -a "${backup_dir}/bin/." "${PREFIX}/bin/"
  [[ -d "${backup_dir}/web" ]] && cp -a "${backup_dir}/web/." "$WEB_DIR/"
  [[ -d "${backup_dir}/etc" ]] && cp -a "${backup_dir}/etc/." "$CONFIG_DIR/"
  [[ -d "${backup_dir}/data" ]] && cp -a "${backup_dir}/data/." "$DATA_DIR/"
  systemctl daemon-reload
  [[ -f /etc/systemd/system/ezhiklb.service ]] && systemctl start ezhiklb.service || true
  [[ -f /etc/systemd/system/ezhiklb-agent.service ]] && systemctl start ezhiklb-agent.service || true
}

[[ "${EUID}" -eq 0 ]] || die "run this installer as root"
[[ -r /etc/os-release ]] || die "unsupported operating system"
. /etc/os-release
case "${ID:-}" in ubuntu|debian) ;; *) die "only Debian and Ubuntu are supported" ;; esac

EXISTING_VERSION=""
[[ -f "$VERSION_FILE" ]] && EXISTING_VERSION="$(<"$VERSION_FILE")"
RECONNECT_REQUESTED=0
if [[ -n "${EZHIKLB_PANEL_URL:-}" && -n "${EZHIKLB_NODE_ID:-}" && -n "${EZHIKLB_AGENT_TOKEN:-}" ]]; then
  RECONNECT_REQUESTED=1
fi

choose_role() {
  if [[ -n "${EZHIKLB_ROLE:-}" ]]; then
    ROLE="$EZHIKLB_ROLE"
    return
  fi
  if [[ -n "$EXISTING_VERSION" ]]; then
    printf 'Existing EzhikLB %s detected. Configuration and database will be preserved.\n' "$EXISTING_VERSION"
    if [[ "${EZHIKLB_YES:-0}" != "1" ]]; then
      read -r -p "Upgrade to ${EZHIKLB_VERSION}? [Y/n]: " confirm
      case "${confirm:-y}" in y|Y|yes|YES) ;; *) die "upgrade cancelled" ;; esac
    fi
    ROLE="$(sed -n 's/^EZHIKLB_ROLE=//p' "$ENV_FILE" 2>/dev/null | tr -d '"' || true)"
    ROLE="${ROLE:-panel-node}"
    return
  fi
  printf '\nВыберите вариант установки:\n'
  printf '  1) Панель\n  2) Нода\n  3) Панель + локальная нода\n'
  read -r -p 'Вариант [3]: ' answer
  case "${answer:-3}" in 1) ROLE="panel" ;; 2) ROLE="node" ;; 3) ROLE="panel-node" ;; *) die "неверный вариант установки" ;; esac
}

choose_role
case "$ROLE" in panel|node|panel-node) ;; *) die "EZHIKLB_ROLE must be panel, node, or panel-node" ;; esac

load_existing_env_value() {
  local key="$1" value=""
  [[ -f "$ENV_FILE" ]] || return 0
  [[ -z "${!key:-}" ]] || return 0
  value="$(sed -n "s/^${key}=//p" "$ENV_FILE" | tail -n 1 | tr -d '\r')"
  [[ -n "$value" ]] || return 0
  printf -v "$key" '%s' "$value"
  export "$key"
}

valid_tcp_port() {
  local value="$1"
  [[ "$value" =~ ^[0-9]+$ ]] && (( value >= 1 && value <= 65535 ))
}

tcp_port_in_use() {
  local port="$1" hex_port=""
  printf -v hex_port '%04X' "$port"
  awk -v wanted="$hex_port" '
    NR > 1 {
      split($2, address, ":")
      if (toupper(address[2]) == wanted && $4 == "0A") found = 1
    }
    END { exit(found ? 0 : 1) }
  ' /proc/net/tcp /proc/net/tcp6 2>/dev/null
}

choose_install_port() {
  local variable="$1" label="$2" default_port="$3" value="" supplied=0
  [[ -n "${!variable:-}" ]] && supplied=1

  while true; do
    if (( supplied )); then
      value="${!variable}"
    else
      read -r -p "${label} [${default_port}]: " value
      value="${value:-$default_port}"
    fi

    if ! valid_tcp_port "$value"; then
      (( supplied )) && die "${label}: порт должен быть числом от 1 до 65535"
      printf 'Порт должен быть числом от 1 до 65535.\n' >&2
      continue
    fi
    if tcp_port_in_use "$value"; then
      (( supplied )) && die "${label}: порт ${value} уже используется"
      printf 'Порт %s уже используется. Укажите другой порт.\n' "$value" >&2
      continue
    fi
    break
  done

  printf -v "$variable" '%s' "$value"
  export "$variable"
}

if [[ -n "$EXISTING_VERSION" ]]; then
  load_existing_env_value EZHIKLB_PANEL_URL
  load_existing_env_value EZHIKLB_NODE_ID
  load_existing_env_value EZHIKLB_AGENT_TOKEN
  load_existing_env_value EZHIKLB_ALLOW_INSECURE
fi

panel_host="${EZHIKLB_HOST:-127.0.0.1}"
if [[ -z "$EXISTING_VERSION" && ( "$ROLE" == "panel" || "$ROLE" == "panel-node" ) && -z "${EZHIKLB_HOST:-}" ]]; then
  printf '\nКак открыть панель?\n'
  printf '  1) Только на сервере и через SSH-туннель (127.0.0.1)\n'
  printf '  2) По сети (0.0.0.0, открывает web-интерфейс извне)\n'
  read -r -p 'Доступ [1]: ' panel_access
  case "${panel_access:-1}" in
    1) panel_host="127.0.0.1" ;;
    2) panel_host="0.0.0.0" ;;
    *) die "неверный вариант доступа к панели" ;;
  esac
fi

panel_port="${EZHIKLB_PORT:-8080}"
agent_port="${EZHIKLB_AGENT_PORT:-8081}"
if [[ -z "$EXISTING_VERSION" && ( "$ROLE" == "panel" || "$ROLE" == "panel-node" ) ]]; then
  agent_port_was_supplied=0
  [[ -n "${EZHIKLB_AGENT_PORT:-}" ]] && agent_port_was_supplied=1
  printf '\nНастройка портов панели:\n'
  choose_install_port EZHIKLB_PORT 'Порт web-панели' 8080
  panel_port="$EZHIKLB_PORT"

  while true; do
    choose_install_port EZHIKLB_AGENT_PORT 'Порт API нод' 8081
    agent_port="$EZHIKLB_AGENT_PORT"
    [[ "$agent_port" != "$panel_port" ]] && break
    (( agent_port_was_supplied )) && die "порты панели и API нод должны различаться"
    printf 'Порт API нод должен отличаться от порта web-панели.\n' >&2
    unset EZHIKLB_AGENT_PORT
  done
fi

if [[ "$ROLE" == "node" ]]; then
  if [[ -z "${EZHIKLB_PANEL_URL:-}" ]]; then
    read -r -p 'URL API нод, доступный с этого сервера: ' EZHIKLB_PANEL_URL
  fi
  if [[ -z "${EZHIKLB_NODE_ID:-}" ]]; then
    read -r -p 'ID ноды из панели: ' EZHIKLB_NODE_ID
  fi
  if [[ -z "${EZHIKLB_AGENT_TOKEN:-}" ]]; then
    read -r -s -p 'Токен ноды из панели: ' EZHIKLB_AGENT_TOKEN
    printf '\n'
  fi
  [[ -n "${EZHIKLB_PANEL_URL:-}" ]] || die "не указан URL API нод"
  [[ -n "${EZHIKLB_NODE_ID:-}" ]] || die "не указан ID ноды"
  [[ ${#EZHIKLB_AGENT_TOKEN} -ge 24 ]] || die "токен ноды должен содержать не менее 24 символов"
  if [[ "$EZHIKLB_PANEL_URL" == http://* && "$EZHIKLB_PANEL_URL" != http://127.0.0.1:* && "$EZHIKLB_PANEL_URL" != http://localhost:* && "${EZHIKLB_ALLOW_INSECURE:-0}" != "1" ]]; then
    printf '\nВнимание: HTTP не шифрует токен и конфигурацию ноды.\n'
    read -r -p 'Разрешить постоянное подключение по HTTP? [y/N]: ' allow_http
    case "${allow_http:-n}" in
      y|Y|yes|YES|д|Д|да|ДА) EZHIKLB_ALLOW_INSECURE=1 ;;
      *) die "для HTTP требуется явное подтверждение" ;;
    esac
  fi
fi

require_artifacts() {
  if [[ "$ROLE" == "panel" || "$ROLE" == "panel-node" ]]; then
    [[ -x "${BUNDLE_DIR}/bin/ezhiklb" ]] || die "missing bin/ezhiklb; use a GitHub release bundle"
    [[ -f "${BUNDLE_DIR}/web/index.html" ]] || die "missing compiled web/index.html; use a GitHub release bundle"
  fi
  if [[ "$ROLE" == "node" || "$ROLE" == "panel-node" ]]; then
    [[ -x "${BUNDLE_DIR}/bin/ezhiklb-agent" ]] || die "missing bin/ezhiklb-agent; use a GitHub release bundle"
  fi
}
require_artifacts

log "Installing system dependencies"
export DEBIAN_FRONTEND=noninteractive
apt-get update
packages=(ca-certificates curl openssl iproute2)
if [[ "$ROLE" == "node" || "$ROLE" == "panel-node" ]]; then
  packages+=(ipvsadm iptables iproute2 iputils-ping conntrack)
fi
apt-get install -y "${packages[@]}"

if [[ -n "$EXISTING_VERSION" ]]; then
  stamp="$(date -u +%Y%m%dT%H%M%SZ)"
  backup_dir="${BACKUP_ROOT}/${stamp}"
  log "Backing up the existing installation to ${backup_dir}"
  systemctl stop ezhiklb-agent.service ezhiklb.service 2>/dev/null || true
  install -d -m 0700 "$backup_dir"
  [[ -d "$CONFIG_DIR" ]] && cp -a "$CONFIG_DIR" "${backup_dir}/etc"
  [[ -d "$DATA_DIR" ]] && cp -a "$DATA_DIR" "${backup_dir}/data"
  [[ -d "${PREFIX}/bin" ]] && cp -a "${PREFIX}/bin" "${backup_dir}/bin"
  [[ -d "$WEB_DIR" ]] && cp -a "$WEB_DIR" "${backup_dir}/web"
fi

log "Preparing users and directories"
if ! getent passwd ezhiklb >/dev/null; then
  useradd --system --home-dir "$DATA_DIR" --shell /usr/sbin/nologin ezhiklb
fi
install -d -m 0750 -o root -g ezhiklb "$CONFIG_DIR"
install -d -m 0750 -o ezhiklb -g ezhiklb "$DATA_DIR"
install -d -m 0750 -o root -g root "$AGENT_DATA_DIR"
# The control plane runs as the unprivileged ezhiklb user. Every directory in
# its executable path must therefore be traversable, while remaining root-owned.
install -d -m 0755 -o root -g root "$PREFIX" "$PREFIX/bin"

legacy_config="/etc/ezhik-udp/ezhik-udp.conf"
if [[ -f "$legacy_config" ]]; then
  install -m 0640 -o root -g ezhiklb "$legacy_config" "${CONFIG_DIR}/legacy-ezhik-udp.conf"
  legacy_config="${CONFIG_DIR}/legacy-ezhik-udp.conf"
fi

if [[ ! -f "$ENV_FILE" ]]; then
  admin_token="${EZHIKLB_ADMIN_TOKEN:-$(openssl rand -hex 32)}"
  agent_token="${EZHIKLB_AGENT_TOKEN:-$(openssl rand -hex 32)}"
  ingress="${EZHIKLB_INGRESS_ADDRESS:-$(detect_server_ipv4)}"
  panel_url="${EZHIKLB_PANEL_URL:-http://127.0.0.1:${agent_port}}"
  cat >"$ENV_FILE" <<EOF
EZHIKLB_ROLE=${ROLE}
EZHIKLB_HOST=${panel_host}
EZHIKLB_PORT=${panel_port}
EZHIKLB_AGENT_HOST=${EZHIKLB_AGENT_HOST:-0.0.0.0}
EZHIKLB_AGENT_PORT=${agent_port}
EZHIKLB_SECURE_COOKIE=0
EZHIKLB_DATABASE=${DATA_DIR}/ezhiklb.db
EZHIKLB_WEB_DIR=${WEB_DIR}
EZHIKLB_INGRESS_ADDRESS=${ingress}
EZHIKLB_ADMIN_TOKEN=${admin_token}
EZHIKLB_AGENT_TOKEN=${agent_token}
EZHIKLB_NODE_ID=${EZHIKLB_NODE_ID:-local}
EZHIKLB_PANEL_URL=${panel_url}
EZHIKLB_ALLOW_INSECURE=${EZHIKLB_ALLOW_INSECURE:-0}
EZHIKLB_AGENT_STATE=${AGENT_DATA_DIR}/state.json
EZHIKLB_LEGACY_CONFIG=${legacy_config}
EOF
  chmod 0640 "$ENV_FILE"
  chown root:ezhiklb "$ENV_FILE"
else
  sed -i "s/^EZHIKLB_ROLE=.*/EZHIKLB_ROLE=${ROLE}/" "$ENV_FILE"
  grep -q '^EZHIKLB_AGENT_HOST=' "$ENV_FILE" || printf 'EZHIKLB_AGENT_HOST=0.0.0.0\n' >>"$ENV_FILE"
  grep -q '^EZHIKLB_AGENT_PORT=' "$ENV_FILE" || printf 'EZHIKLB_AGENT_PORT=8081\n' >>"$ENV_FILE"
  if [[ "$ROLE" == "node" ]]; then
    sed -i '/^EZHIKLB_PANEL_URL=/d;/^EZHIKLB_NODE_ID=/d;/^EZHIKLB_AGENT_TOKEN=/d;/^EZHIKLB_ALLOW_INSECURE=/d' "$ENV_FILE"
    printf 'EZHIKLB_PANEL_URL=%s\n' "$EZHIKLB_PANEL_URL" >>"$ENV_FILE"
    printf 'EZHIKLB_NODE_ID=%s\n' "$EZHIKLB_NODE_ID" >>"$ENV_FILE"
    printf 'EZHIKLB_AGENT_TOKEN=%s\n' "$EZHIKLB_AGENT_TOKEN" >>"$ENV_FILE"
    printf 'EZHIKLB_ALLOW_INSECURE=%s\n' "${EZHIKLB_ALLOW_INSECURE:-0}" >>"$ENV_FILE"
  fi
fi

if [[ "$ROLE" == "panel" || "$ROLE" == "panel-node" ]]; then
  log "Installing panel ${EZHIKLB_VERSION}"
  install -m 0755 "${BUNDLE_DIR}/bin/ezhiklb" "${PREFIX}/bin/ezhiklb"
  install -d -m 0755 "$WEB_DIR"
  cp -a "${BUNDLE_DIR}/web/." "$WEB_DIR/"
  chown -R root:root "$WEB_DIR"
  cat >/etc/systemd/system/ezhiklb.service <<EOF
[Unit]
Description=EzhikLB control plane
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=ezhiklb
Group=ezhiklb
EnvironmentFile=${ENV_FILE}
ExecStart=${PREFIX}/bin/ezhiklb
Restart=on-failure
RestartSec=3s
NoNewPrivileges=yes
PrivateTmp=yes
ProtectHome=yes
ProtectSystem=strict
ReadWritePaths=${DATA_DIR}

[Install]
WantedBy=multi-user.target
EOF
fi

legacy_was_active=0
if [[ "$ROLE" == "node" || "$ROLE" == "panel-node" ]]; then
  log "Installing node agent ${EZHIKLB_VERSION}"
  install -m 0755 "${BUNDLE_DIR}/bin/ezhiklb-agent" "${PREFIX}/bin/ezhiklb-agent"
  cat >/etc/modules-load.d/ezhiklb.conf <<'EOF'
ip_vs
ip_vs_rr
ip_vs_wrr
nf_conntrack
xt_ipvs
EOF
  cat >/etc/sysctl.d/98-ezhiklb.conf <<'EOF'
net.ipv4.ip_forward = 1
net.ipv4.vs.conntrack = 1
net.ipv4.vs.snat_reroute = 1
net.ipv4.vs.expire_nodest_conn = 1
net.ipv4.vs.expire_quiescent_template = 1
net.ipv4.conf.all.rp_filter = 2
net.ipv4.conf.default.rp_filter = 2
EOF
  modprobe ip_vs ip_vs_rr ip_vs_wrr nf_conntrack xt_ipvs
  sysctl --load /etc/sysctl.d/98-ezhiklb.conf >/dev/null
  cat >/etc/systemd/system/ezhiklb-agent.service <<EOF
[Unit]
Description=EzhikLB node agent
After=network-online.target ezhiklb.service
Wants=network-online.target

[Service]
Type=simple
EnvironmentFile=${ENV_FILE}
ExecStart=${PREFIX}/bin/ezhiklb-agent
Restart=on-failure
RestartSec=3s
PrivateTmp=yes
ProtectHome=yes
ProtectSystem=strict
ReadWritePaths=${AGENT_DATA_DIR} ${PREFIX}/bin

[Install]
WantedBy=multi-user.target
EOF
  if systemctl is-active --quiet ezhik-udp.service 2>/dev/null; then
    legacy_was_active=1
    log "Stopping legacy Ezhik UDP for an atomic handover; its configuration is preserved"
    systemctl stop ezhik-udp.service
  fi
fi

printf '%s\n' "$EZHIKLB_VERSION" >"$VERSION_FILE"
systemctl daemon-reload

if [[ "$ROLE" == "panel" || "$ROLE" == "panel-node" ]]; then
  systemctl enable ezhiklb.service
  systemctl restart ezhiklb.service
  installed_panel_port="$(sed -n 's/^EZHIKLB_PORT=//p' "$ENV_FILE")"
  installed_panel_port="${installed_panel_port:-8080}"
  for _ in {1..20}; do
    curl -fsS "http://127.0.0.1:${installed_panel_port}/healthz" >/dev/null 2>&1 && break
    sleep 1
  done
  if ! systemctl is-active --quiet ezhiklb.service; then
    systemctl status ezhiklb.service --no-pager || true
    restore_previous_release
    [[ "$legacy_was_active" == "1" ]] && systemctl start ezhik-udp.service || true
    die "panel health check failed"
  fi
fi

if [[ "$ROLE" == "node" || "$ROLE" == "panel-node" ]]; then
  systemctl enable ezhiklb-agent.service
  systemctl restart ezhiklb-agent.service
  sleep 2
  if ! systemctl is-active --quiet ezhiklb-agent.service; then
    systemctl status ezhiklb-agent.service --no-pager || true
    restore_previous_release
    [[ "$legacy_was_active" == "1" ]] && systemctl start ezhik-udp.service || true
    die "agent failed to start; legacy service was restored when applicable"
  fi
  if [[ -z "$EXISTING_VERSION" || "$RECONNECT_REQUESTED" == "1" ]]; then
    apply_marker="/run/ezhiklb-agent-install.$$"
    : >"$apply_marker"
    systemctl restart ezhiklb-agent.service
    for _ in {1..30}; do
      [[ -s "${AGENT_DATA_DIR}/state.json" && "${AGENT_DATA_DIR}/state.json" -nt "$apply_marker" ]] && break
      sleep 1
    done
    if [[ ! -s "${AGENT_DATA_DIR}/state.json" || ! "${AGENT_DATA_DIR}/state.json" -nt "$apply_marker" ]]; then
      journalctl -u ezhiklb-agent.service -n 80 --no-pager || true
      systemctl stop ezhiklb-agent.service || true
      restore_previous_release
      [[ "$legacy_was_active" == "1" ]] && systemctl start ezhik-udp.service || true
      die "agent did not apply its first revision; legacy service was restored when applicable"
    fi
    rm -f -- "$apply_marker"
  else
    log "Agent updated; panel availability will be checked in the background"
  fi
fi

log "${EZHIKLB_VERSION} installed successfully"
printf 'Role: %s\n' "$ROLE"
if [[ "$ROLE" == "panel" || "$ROLE" == "panel-node" ]]; then
  installed_host="$(sed -n 's/^EZHIKLB_HOST=//p' "$ENV_FILE")"
  installed_panel_port="$(sed -n 's/^EZHIKLB_PORT=//p' "$ENV_FILE")"
  installed_agent_port="$(sed -n 's/^EZHIKLB_AGENT_PORT=//p' "$ENV_FILE")"
  installed_panel_port="${installed_panel_port:-8080}"
  installed_agent_port="${installed_agent_port:-8081}"
  panel_ipv4="$(sed -n 's/^EZHIKLB_INGRESS_ADDRESS=//p' "$ENV_FILE")"
  if [[ -z "$panel_ipv4" || "$panel_ipv4" == "0.0.0.0" || "$panel_ipv4" == "127.0.0.1" ]]; then
    panel_ipv4="$(detect_server_ipv4)"
  fi
  if [[ "$installed_host" == "127.0.0.1" ]]; then
    printf 'Local panel: http://127.0.0.1:%s\n' "$installed_panel_port"
    printf 'SSH tunnel: ssh -L %s:127.0.0.1:%s root@YOUR_SERVER\n' "$installed_panel_port" "$installed_panel_port"
  else
    if [[ -n "$panel_ipv4" ]]; then
      printf 'Open in browser: http://%s:%s\n' "$panel_ipv4" "$installed_panel_port"
    else
      printf 'IPv4 detection failed. Set EZHIKLB_INGRESS_ADDRESS in %s and restart the panel.\n' "$ENV_FILE"
    fi
  fi
  if [[ -n "$panel_ipv4" ]]; then
    printf 'Node API: http://%s:%s\n' "$panel_ipv4" "$installed_agent_port"
  fi
  printf 'Admin token: %s\n' "$(sed -n 's/^EZHIKLB_ADMIN_TOKEN=//p' "$ENV_FILE")"
fi
printf 'Configuration: %s\n' "$ENV_FILE"
