"""Settings of the radio service and the emulator, taken from the environment."""
import os
from pathlib import Path

HOME = Path(os.environ.get("KUBENDT_RADIO_HOME") or Path.home() / "kubendt-radio").expanduser()
ZONES = HOME / "zones"
KUBENDT_URL = (os.environ.get("KUBENDT_URL") or "http://localhost:8080").rstrip("/")
OPEN5GS_IMAGE = os.environ.get("KUBENDT_RADIO_OPEN5GS_IMAGE") or "docker_open5gs:latest"
UERANSIM_IMAGE = os.environ.get("KUBENDT_RADIO_UERANSIM_IMAGE") or "gradiant/ueransim:3.2.6"
TOOLBOX_IMAGE = os.environ.get("KUBENDT_RADIO_TOOLBOX_IMAGE") or "ghcr.io/emigcm98/kubendt-toolbox:latest"


def token():
    """The KubeNDT API token: KUBENDT_TOKEN, or the content of KUBENDT_TOKEN_FILE."""
    value = os.environ.get("KUBENDT_TOKEN")
    if value:
        return value.strip()
    path = Path(os.environ.get("KUBENDT_TOKEN_FILE") or HOME / ".kubendt_token").expanduser()
    return path.read_text().strip()
