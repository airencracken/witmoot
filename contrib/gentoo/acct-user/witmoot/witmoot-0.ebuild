# Copyright 2026 Marcus J. Hildum

EAPI=8

inherit acct-user

DESCRIPTION="Service account for Witmoot"
KEYWORDS="~amd64 ~arm64"
ACCT_USER_ID=-1
ACCT_USER_GROUPS=( witmoot )
ACCT_USER_HOME=/var/lib/witmoot
ACCT_USER_HOME_PERMS=0700

acct-user_add_deps
