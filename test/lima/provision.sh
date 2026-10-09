#!/bin/sh
# Provision a cronman test host with known-broken cron jobs. Run as root.
# Arg: distro family (debian|rocky|alpine). Needs the SSH public key at /tmp/lima.pub.
#   limactl copy test/lima/provision.sh cm-debian:/tmp/ && limactl copy ~/.lima/_config/user.pub cm-debian:/tmp/lima.pub
#   limactl shell cm-debian sudo sh /tmp/provision.sh debian
# Users: alice (no sudo, owns the broken crontab), bob (sudo with password pw-bob), carol (passwordless sudo).
set -e
fam=$1
case $fam in
  debian) apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq cron sudo >/dev/null; systemctl enable --now cron; SUDOGRP=sudo ;;
  rocky)  dnf -y -q install cronie sudo >/dev/null; systemctl enable --now crond; SUDOGRP=wheel ;;
  alpine) apk add -q sudo shadow; rc-update add crond default >/dev/null 2>&1 || true; rc-service crond start || true; SUDOGRP=wheel; addgroup -S wheel 2>/dev/null || true ;;
esac
KEY=$(cat /tmp/lima.pub)
mk() { # user
  id "$1" >/dev/null 2>&1 || { if [ $fam = alpine ]; then adduser -D -s /bin/sh "$1"; else useradd -m -s /bin/bash "$1"; fi; }
  echo "$1:pw-$1" | chpasswd
  if [ $fam = alpine ]; then passwd -u "$1" >/dev/null 2>&1 || true; fi
  h=$(eval echo ~"$1"); mkdir -p "$h/.ssh"; echo "$KEY" > "$h/.ssh/authorized_keys"
  chown -R "$1:" "$h/.ssh"; chmod 700 "$h/.ssh"; chmod 600 "$h/.ssh/authorized_keys"
}
mk alice; mk bob; mk carol
echo 'bob ALL=(ALL) ALL' > /etc/sudoers.d/bob          # sudo with password
echo 'carol ALL=(ALL) NOPASSWD: ALL' > /etc/sudoers.d/carol
chmod 440 /etc/sudoers.d/bob /etc/sudoers.d/carol

A=$(eval echo ~alice)
mkdir -p $A/bin $A/logs
printf '#!/bin/sh\necho ok\n' > $A/bin/good.sh; chmod 755 $A/bin/good.sh
printf '#!/bin/sh\necho noexec-bit\n' > $A/bin/noexec.sh; chmod 644 $A/bin/noexec.sh
printf '#!/bin/sh\r\necho crlf\r\n' > $A/bin/crlf.sh; chmod 755 $A/bin/crlf.sh
printf '#!/usr/bin/env node\nconsole.log(1)\n' > $A/bin/node.js; chmod 755 $A/bin/node.js
mkdir -p /usr/local/bin; printf '#!/bin/sh\necho fake-node\n' > /usr/local/bin/node; chmod 755 /usr/local/bin/node
printf '#!/bin/sh\necho root-job\n' > $A/bin/rootjob.sh; chmod 755 $A/bin/rootjob.sh
mkdir -p /var/log/locked; chmod 755 /var/log/locked
# noexec mount
mkdir -p /mnt/noexec; mountpoint -q /mnt/noexec || mount -t tmpfs -o noexec,size=1m tmpfs /mnt/noexec
printf '#!/bin/sh\necho x\n' > /mnt/noexec/s.sh; chmod 755 /mnt/noexec/s.sh
chown -R alice: $A/bin $A/logs

cat > /tmp/alice.cron <<CR
MAILTO=""
*/5 * * * * $A/bin/good.sh >> $A/logs/good.log 2>&1
0 1 * * * $A/bin/missing.sh
0 2 * * * $A/bin/noexec.sh
0 3 * * * $A/bin/crlf.sh
0 4 * * * $A/bin/node.js
0 5 * * * /mnt/noexec/s.sh
0 6 * * * $A/bin/good.sh >> /var/log/locked/out.log
0 7 * * * tar czf /tmp/bk-\$(date +%F).tgz $A/bin
#30 8 * * 1 $A/bin/good.sh
CR
crontab -u alice /tmp/alice.cron
# root runs a script alice can write (busybox crond has no /etc/cron.d)
if [ -d /etc/cron.d ]; then
  echo "15 3 * * * root $A/bin/rootjob.sh" > /etc/cron.d/cmtest; chmod 644 /etc/cron.d/cmtest
else
  (crontab -l -u root 2>/dev/null; echo "15 3 * * * $A/bin/rootjob.sh") | crontab -u root -
fi
echo provisioned
