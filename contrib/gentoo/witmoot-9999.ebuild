# Copyright 2026 Marcus J. Hildum
# SPDX-License-Identifier: AGPL-3.0-or-later
# Live ebuild for the master branch; see docs/deployment.md for overlay setup.

EAPI=8

inherit git-r3 go-module systemd

DESCRIPTION="A small, self-hosted bulletin board for friends and family"
HOMEPAGE="https://github.com/airencracken/witmoot"
EGIT_REPO_URI="https://github.com/airencracken/witmoot.git"
EGIT_BRANCH="master"

# Witmoot uses AGPL-3.0-or-later. The remaining entries cover the linked
# Go dependencies, their bundled code, and the embedded HTMX asset.
LICENSE="AGPL-3+ 0BSD BSD BSD-2 MIT public-domain"
SLOT="0"
KEYWORDS=""
PROPERTIES="live"
IUSE="test"
RESTRICT="!test? ( test )"
DOCS=( LICENSE README.md THIRD_PARTY.md )

RDEPEND="
	acct-group/witmoot
	acct-user/witmoot
	app-admin/logrotate
	app-misc/ca-certificates
"
BDEPEND+="
	>=dev-lang/go-1.26
	acct-group/witmoot
	acct-user/witmoot
	test? ( app-admin/logrotate )
"

src_unpack() {
	git-r3_src_unpack
	go-module_live_vendor
}

src_configure() {
	go-module_src_configure
}

src_compile() {
	CGO_ENABLED=0 ego build -buildvcs=false -trimpath -o witmoot ./cmd/witmoot
}

src_test() {
	ego test -count=1 ./...
}

src_install() {
	dobin witmoot
	einstalldocs
	dodoc -r docs

	keepdir /var/lib/witmoot
	fowners witmoot:witmoot /var/lib/witmoot
	fperms 0700 /var/lib/witmoot

	sed 's|/usr/local/bin/witmoot|/usr/bin/witmoot|g' \
		contrib/openrc/witmoot > "${T}/witmoot.initd" || die
	newinitd "${T}/witmoot.initd" witmoot
	newconfd contrib/openrc/witmoot.confd witmoot
	fperms 0600 /etc/conf.d/witmoot
	insinto /etc/logrotate.d
	newins contrib/logrotate/witmoot witmoot

	sed 's|/usr/local/bin/witmoot|/usr/bin/witmoot|g' \
		contrib/systemd/witmoot.service > "${T}/witmoot.service" || die
	systemd_dounit "${T}/witmoot.service"
	insinto /etc/witmoot
	newins contrib/systemd/witmoot.env witmoot.env
	fperms 0600 /etc/witmoot/witmoot.env
}

pkg_postinst() {
	elog "Configure /etc/conf.d/witmoot (OpenRC) or /etc/witmoot/witmoot.env (systemd)."
	elog "The native service listens on 127.0.0.1:8082 and uses /var/lib/witmoot."
	elog "Provision an owner before starting; see docs/deployment.md in the source tree."
	elog "OpenRC logs need logrotate's cron job or timer enabled."
}
