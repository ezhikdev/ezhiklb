#!/usr/bin/env bash
set -Eeuo pipefail

die() { printf 'EzhikLB uninstall error: %s\n' "$*" >&2; exit 1; }
[[ "$EUID" -eq 0 ]] || die 'run as root'
case "${1:-}" in
  --yes) ;;
  '') read -r -p 'Удалить панель EzhikLB, базу, настройки и локальную ноду? [y/N]: ' answer
      case "$answer" in y|Y|yes|YES|да|ДА) ;; *) exit 0 ;; esac ;;
  *) die 'usage: uninstall-panel.sh [--yes]' ;;
esac

env_file=/etc/ezhiklb/ezhiklb.env
if [[ -f "$env_file" ]] && grep -qx 'EZHIKLB_NODE_ID=local' "$env_file" && [[ -f /etc/systemd/system/ezhiklb-agent.service ]]; then
  [[ -f /opt/ezhiklb/uninstall-node.sh ]] || die 'uninstall-node.sh missing; restore the complete v1.2.1 release'
  bash /opt/ezhiklb/uninstall-node.sh --yes
fi
if [[ -f /etc/systemd/system/ezhiklb.service ]]; then
  systemctl stop ezhiklb.service
fi
systemctl disable ezhiklb.service 2>/dev/null || true
rm -f -- /etc/systemd/system/ezhiklb.service /opt/ezhiklb/bin/ezhiklb
rm -rf -- /var/lib/ezhiklb /usr/share/ezhiklb/web
systemctl daemon-reload
systemctl reset-failed ezhiklb.service 2>/dev/null || true
if [[ -f /etc/systemd/system/ezhiklb-agent.service ]]; then
  # Preserve a separately enrolled remote agent on the same host.
  sed -i 's/^EZHIKLB_ROLE=.*/EZHIKLB_ROLE=node/' "$env_file"
else
  rm -rf -- /etc/ezhiklb
  rm -f -- /opt/ezhiklb/uninstall-node.sh /opt/ezhiklb/uninstall-panel.sh
  rmdir /opt/ezhiklb/bin /opt/ezhiklb /usr/share/ezhiklb 2>/dev/null || true
  rmdir /var/lib/ezhiklb-agent 2>/dev/null || true
  if getent passwd ezhiklb >/dev/null; then
    userdel ezhiklb
  fi
fi
printf 'Панель удалена. Удалённые ноды и резервные копии /var/backups/ezhiklb сохранены.\n'
