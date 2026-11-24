#!/usr/bin/env bash
# Deploys GotATicket on this Ubuntu server: sudo ./deploy.sh   (options: sudo ./deploy.sh --help)
exec "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/scripts/deploy-ubuntu.sh" "$@"
