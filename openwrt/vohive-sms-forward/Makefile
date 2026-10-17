include $(TOPDIR)/rules.mk

PKG_NAME:=vohive-sms-forward
PKG_VERSION:=0.2.0
PKG_RELEASE:=1
PKG_LICENSE:=MIT
PKG_LICENSE_FILES:=LICENSE
PKG_BUILD_DEPENDS:=golang/host
PKG_BUILD_PARALLEL:=1
PKG_BUILD_FLAGS:=no-mips16

# Build an explicit local source snapshot, including uncommitted migration
# changes. See docs/openwrt.md; no download of an unrelated upstream release.
VOHIVE_SOURCE_DIR?=

GO_PKG:=github.com/ABreadTree/vohive-sms-forward
GO_PKG_BUILD_PKG:=$(GO_PKG)/cmd/vohive-mac
GO_PKG_INSTALL_ALL:=1
GO_PKG_TAGS:=sqlite_omit_load_extension

include $(INCLUDE_DIR)/package.mk
include $(TOPDIR)/feeds/packages/lang/golang/golang-package.mk

define Package/vohive-sms-forward
  SECTION:=net
  CATEGORY:=Network
  TITLE:=SMS-only USB gateway with Bark/Telegram forwarding
  URL:=https://github.com/ABreadTree/vohive-sms-forward
  DEPENDS:=$(GO_ARCH_DEPENDS) +libusb-1.0 +ca-bundle +libpthread
endef

define Package/vohive-sms-forward/description
  Embedded web console, SQLite SMS archive, and Bark/Telegram notifications.
  Fixed SMS-only procd service; notifications use the router network.
  Raw USB AT access; no modem networking, systemd, bash or QMI daemon required.
endef

define Package/vohive-sms-forward/conffiles
/etc/config/vohive-sms-forward
/etc/vohive-sms-forward/
endef

define Build/Prepare
	[ -f "$(VOHIVE_SOURCE_DIR)/go.mod" ] || { echo "Set VOHIVE_SOURCE_DIR to the absolute project path" >&2; exit 1; }
	mkdir -p $(PKG_BUILD_DIR)
	$(CP) $(VOHIVE_SOURCE_DIR)/go.mod $(VOHIVE_SOURCE_DIR)/go.sum $(VOHIVE_SOURCE_DIR)/LICENSE $(PKG_BUILD_DIR)/
	$(CP) $(VOHIVE_SOURCE_DIR)/cmd $(VOHIVE_SOURCE_DIR)/internal $(PKG_BUILD_DIR)/
endef

define Package/vohive-sms-forward/install
	$(INSTALL_DIR) $(1)/usr/bin $(1)/etc/init.d $(1)/etc/config
	$(INSTALL_DIR) $(1)/etc/vohive-sms-forward
	chmod 0700 $(1)/etc/vohive-sms-forward
	$(INSTALL_BIN) $(GO_PKG_BUILD_BIN_DIR)/vohive-mac $(1)/usr/bin/vohive-sms-forward
	$(INSTALL_BIN) ./files/vohive-sms-forward.init $(1)/etc/init.d/vohive-sms-forward
	$(INSTALL_CONF) ./files/vohive-sms-forward.config $(1)/etc/config/vohive-sms-forward
endef

$(eval $(call GoBinPackage,vohive-sms-forward))
$(eval $(call BuildPackage,vohive-sms-forward))
