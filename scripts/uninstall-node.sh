#!/usr/bin/env bash
set -Eeuo pipefail

die() { printf 'EzhikLB uninstall error: %s\n' "$*" >&2; exit 1; }
[[ "$EUID" -eq 0 ]] || die 'run as root'
case "${1:-}" in
  --yes) ;;
  '') read -r -p 'Удалить ноду EzhikLB, её маршруты и файлы? [y/N]: ' answer
      case "$answer" in y|Y|yes|YES|да|ДА) ;; *) exit 0 ;; esac ;;
  *) die 'usage: uninstall-node.sh [--yes]' ;;
esac

env_file=/etc/ezhiklb/ezhiklb.env
agent_bin=/opt/ezhiklb/bin/ezhiklb-agent
if [[ -f /etc/systemd/system/ezhiklb-agent.service || -d /var/lib/ezhiklb-agent ]]; then
  [[ -x "$agent_bin" ]] || die 'agent binary missing; restore v1.2.1 before removing managed network state'
  # Old agents ignore CLI arguments. Bound the version query and refuse to
  # delete ownership state unless the cleanup-capable binary is installed.
  installed_version="$(timeout 3 env -u EZHIKLB_AGENT_TOKEN "$agent_bin" --version 2>/dev/null || true)"
  [[ "$installed_version" =~ ^1\.2\.([1-9][0-9]*)$ || "$installed_version" =~ ^1\.([3-9]|[1-9][0-9]+)\. || "$installed_version" =~ ^([2-9]|[1-9][0-9]+)\. ]] || die 'install the full v1.2.1+ release before uninstalling'
  [[ -f "$env_file" ]] || die 'agent environment missing; restore it before cleanup'
  # Parse only known values; never execute an environment file as shell code.
  while IFS='=' read -r key value; do
    value="${value%$'\r'}"
    case "$key" in
      EZHIKLB_NODE_ID|EZHIKLB_PANEL_URL|EZHIKLB_AGENT_TOKEN|EZHIKLB_AGENT_STATE|EZHIKLB_ALLOW_INSECURE)
        export "$key=$value" ;;
    esac
  done <"$env_file"
  [[ "${EZHIKLB_AGENT_STATE:-/var/lib/ezhiklb-agent/state.json}" == /var/lib/ezhiklb-agent/state.json ]] || die 'custom agent state path: use a manual uninstall to preserve ownership state'
  systemctl stop ezhiklb-agent.service
  timeout 90 "$agent_bin" --uninstall || die 'cleanup failed; files retained for retry'
fi

for service in ezhiklb-agent ezhiklb-haproxy; do
  if [[ -f "/etc/systemd/system/${service}.service" ]]; then
    systemctl stop "${service}.service"
  fi
done
systemctl disable ezhiklb-agent.service ezhiklb-haproxy.service 2>/dev/null || true
rm -f -- /etc/systemd/system/ezhiklb-agent.service /etc/systemd/system/ezhiklb-haproxy.service
rm -f -- /etc/modules-load.d/ezhiklb.conf /etc/sysctl.d/98-ezhiklb.conf "$agent_bin"
rm -rf -- /var/lib/ezhiklb-agent /run/ezhiklb-haproxy
systemctl daemon-reload
systemctl reset-failed ezhiklb-agent.service ezhiklb-haproxy.service 2>/dev/null || true
if [[ -f /etc/systemd/system/ezhiklb.service ]]; then
  # The panel and local agent share their environment and administrator token.
  sed -i 's/^EZHIKLB_ROLE=.*/EZHIKLB_ROLE=panel/' "$env_file"
  systemctl restart ezhiklb.service
else
  rm -rf -- /etc/ezhiklb
  rm -f -- /opt/ezhiklb/uninstall-node.sh /opt/ezhiklb/uninstall-panel.sh
  rmdir /opt/ezhiklb/bin /opt/ezhiklb 2>/dev/null || true
  rmdir /var/lib/ezhiklb 2>/dev/null || true
  if getent passwd ezhiklb >/dev/null; then
    userdel ezhiklb
  fi
fi
printf 'Нода удалена. Общие системные пакеты и резервные копии сохранены.\n'
