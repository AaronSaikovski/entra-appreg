"""Azure CLI stand-in used only by offline process-level tests."""

import json
import os
from pathlib import Path
import sys


Path(os.environ["AUTH_MARKER"]).touch()
if os.environ.get("TEST_AZ_SUCCESS") != "1":
    sys.exit(1)

if sys.argv[1:3] == ["account", "show"]:
    print("11111111-1111-1111-1111-111111111111")
elif sys.argv[1:3] == ["account", "get-access-token"]:
    print(json.dumps({
        "accessToken": "offline-test-token",
        "expires_on": 4102444800,
        "expiresOn": "2100-01-01 00:00:00.000000",
        "tokenType": "Bearer",
    }))
else:
    sys.exit(1)
