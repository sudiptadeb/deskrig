#!/bin/bash
# Builds a clean Arch Linux ARM disk for a Tart VM: KDE Plasma on Wayland, logged straight in as
# admin (password admin, sudo without one), Tart's guest agent, sshd and Flatpak. Cirrus Labs has
# no plain Arch image, and the Omarchy one brings its own desktop services, so deskrig's Arch
# target starts from this. It runs as root on an Arch Linux ARM machine (an Omarchy VM will do):
#
#   sudo ./build-image.sh <out.img> <tart-guest-agent> <tart-guest-agent.service>
#
# The agent and its unit come from the linux arm64 .deb of github.com/cirruslabs/tart-guest-agent.
# Packages come over https only (our network allows 443 alone): Omarchy's mirror of Arch Linux ARM.
# Then, on the Mac: `tart create --linux <vm>`, replace its disk.img with out.img, and
# `deskrig vm create <vm> --desktop kde`.
set -euo pipefail
out=${1:?usage: $0 <out.img> <tart-guest-agent> <tart-guest-agent.service>}
agent=${2:?}
unit=${3:?}
mirror='https://arm-mirror.omarchy.org/$arch/$repo'
size=40G

pacman -S --needed --noconfirm arch-install-scripts gptfdisk dosfstools e2fsprogs >/dev/null

work=$(mktemp -d /var/tmp/deskrig-arch.XXXXXX)
root=$work/root
loop=
cleanup() {
  umount -R "$root" 2>/dev/null || true
  [ -n "$loop" ] && losetup -d "$loop" 2>/dev/null || true
  # Never delete through a mount: that would empty the image.
  mountpoint -q "$root" || rm -rf --one-file-system "$work"
}
trap cleanup EXIT

cat > "$work/pacman.conf" <<CONF
[options]
Architecture = aarch64
SigLevel = Required DatabaseOptional
LocalFileSigLevel = Optional
ParallelDownloads = 8

[core]
Server = $mirror
[extra]
Server = $mirror
[alarm]
Server = $mirror
CONF

rm -f "$out"
truncate -s $size "$out"
sgdisk -Z -n1:0:+1G -t1:ef00 -c1:ESP -n2:0:0 -t2:8305 -c2:root "$out" >/dev/null
loop=$(losetup -fP --show "$out")
mkfs.vfat -F 32 -n ESP "${loop}p1" >/dev/null
mkfs.ext4 -q -L root "${loop}p2"
mkdir -p "$root"
mount "${loop}p2" "$root"
mkdir -p "$root/boot"
mount "${loop}p1" "$root/boot"

pacstrap -C "$work/pacman.conf" -K "$root" base linux-aarch64 archlinuxarm-keyring sudo openssh \
  plasma-desktop kwin sddm konsole flatpak xdg-desktop-portal-kde plasma-workspace breeze \
  noto-fonts dbus-broker-units

cp "$work/pacman.conf" "$root/etc/pacman.conf"
printf 'Server = %s\n' "$mirror" > "$root/etc/pacman.d/mirrorlist"
sed -i 's#^Server = .*#Include = /etc/pacman.d/mirrorlist#' "$root/etc/pacman.conf"
cat > "$root/etc/fstab" <<FSTAB
LABEL=root / ext4 rw,relatime 0 1
LABEL=ESP /boot vfat rw,relatime,umask=0077 0 2
FSTAB
echo deskrig-arch > "$root/etc/hostname"
echo 'en_US.UTF-8 UTF-8' > "$root/etc/locale.gen"
echo 'LANG=en_US.UTF-8' > "$root/etc/locale.conf"
echo 'KEYMAP=us' > "$root/etc/vconsole.conf"
ln -sf /usr/share/zoneinfo/UTC "$root/etc/localtime"
cat > "$root/etc/systemd/network/20-wired.network" <<NET
[Match]
Name=en* eth*
[Network]
DHCP=yes
NET
install -m 755 "$agent" "$root/usr/bin/tart-guest-agent"
install -m 644 "$unit" "$root/etc/systemd/system/tart-guest-agent.service"
# The VM's devices are virtio; the image must boot without detecting them first.
sed -i 's/^MODULES=.*/MODULES=(virtio_blk virtio_pci virtio_net virtio_gpu virtiofs)/; s/ autodetect//' \
  "$root/etc/mkinitcpio.conf"

arch-chroot "$root" /bin/bash -euo pipefail <<'CHROOT'
locale-gen >/dev/null
pacman-key --init >/dev/null
pacman-key --populate archlinuxarm >/dev/null
mkinitcpio -P >/dev/null
useradd -m -G wheel -s /bin/bash admin
echo admin:admin | chpasswd
echo '%wheel ALL=(ALL:ALL) NOPASSWD: ALL' > /etc/sudoers.d/wheel
chmod 440 /etc/sudoers.d/wheel
systemctl enable systemd-networkd systemd-resolved sshd sddm tart-guest-agent >/dev/null 2>&1
CHROOT
ln -sf /run/systemd/resolve/stub-resolv.conf "$root/etc/resolv.conf"

# The Mac's EFI boots EFI/BOOT/BOOTAA64.EFI: systemd-boot, which starts the kernel's EFI stub.
bootctl --esp-path="$root/boot" --no-variables install >/dev/null
kernel=
for k in Image Image.gz vmlinuz-linux-aarch64 vmlinuz-linux; do
  [ -f "$root/boot/$k" ] && { kernel=$k; break; }
done
[ -n "$kernel" ] || { echo "no kernel in /boot: $(ls "$root/boot")" >&2; exit 1; }
mkdir -p "$root/boot/loader/entries"
cat > "$root/boot/loader/loader.conf" <<LOADER
default arch.conf
timeout 0
LOADER
cat > "$root/boot/loader/entries/arch.conf" <<ENTRY
title Arch Linux ARM
linux /$kernel
initrd /initramfs-linux.img
options root=LABEL=root rw quiet
ENTRY
echo "built $out: $(du -h "$out" | cut -f1) used of $size, kernel $kernel"
