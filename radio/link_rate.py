"""From SINR to the rate a radio link carries: Sionna SYS link adaptation, evaluated once and cached."""
from dataclasses import dataclass
from pathlib import Path

import numpy as np


@dataclass(frozen=True)
class LinkConfig:
    """Carrier, frame and receiver parameters of the radio link model."""
    bandwidth_hz: float = 100e6
    subcarrier_spacing_hz: float = 30e3
    num_prb: int = 273
    downlink_fraction: float = 0.7
    overhead: float = 0.14
    downlink_layers: int = 2
    uplink_layers: int = 1
    bler_target: float = 0.1
    ue_power_dbm: float = 23.0
    ue_noise_figure_db: float = 7.0
    bs_noise_figure_db: float = 5.0
    interference_activity: float = 0.5
    uplink_interference_margin_db: float = 3.0
    sinr_grid_db: tuple = (-12.0, 35.0, 0.25)

    @property
    def resource_elements_per_s(self):
        slots_per_s = 1000.0 * self.subcarrier_spacing_hz / 15e3
        return self.num_prb * 12 * 14 * slots_per_s * (1.0 - self.overhead)

    def noise_dbm(self, noise_figure_db):
        """Thermal noise over the carrier plus the receiver noise figure."""
        return -174.0 + 10.0 * np.log10(self.bandwidth_hz) + noise_figure_db


def _efficiency_curve(sinr_db, table, category, bler_target):
    """Expected delivered bits per resource element under inner-loop link adaptation."""
    import sionna.sys as sys_
    import torch

    phy = sys_.PHYAbstraction()
    illa = sys_.InnerLoopLinkAdaptation(phy, bler_target=bler_target)
    num_re = 50 * 12 * 12
    sinr = torch.as_tensor(10.0 ** (sinr_db / 10.0), dtype=torch.float32)
    re = torch.full(sinr.shape, num_re, dtype=torch.int32)
    kwargs = dict(num_allocated_re=re, mcs_table_index=table, mcs_category=category)
    mcs = illa(sinr_eff=sinr, **kwargs)
    tbler = phy(mcs, sinr_eff=sinr, **kwargs)[4]
    delivered = phy(mcs, sinr_eff=torch.full_like(sinr, 1e6), **kwargs)[0]
    efficiency = delivered.double().cpu().numpy() * (1.0 - tbler.double().cpu().numpy()) / num_re
    return np.maximum.accumulate(efficiency)


class LinkRate:
    """SINR [dB] to spectral efficiency to rate, with the curves cached on disk."""

    def __init__(self, config, cache):
        self.config = config
        cache = Path(cache)
        lo, hi, step = config.sinr_grid_db
        self.grid = np.arange(lo, hi + 1e-9, step)
        key = repr((config.sinr_grid_db, config.bler_target))
        if cache.exists() and str(np.load(cache)["key"]) == key:
            z = np.load(cache)
            self.downlink, self.uplink = z["downlink"], z["uplink"]
        else:
            self.downlink = _efficiency_curve(self.grid, table=2, category=1, bler_target=config.bler_target)
            self.uplink = _efficiency_curve(self.grid, table=1, category=0, bler_target=config.bler_target)
            cache.parent.mkdir(parents=True, exist_ok=True)
            np.savez(cache, key=np.array(key), grid=self.grid, downlink=self.downlink, uplink=self.uplink)

    def downlink_capacity_mbps(self, sinr_db):
        """Rate of a user that holds every downlink resource of its sector."""
        c = self.config
        se = np.interp(sinr_db, self.grid, self.downlink, left=0.0)
        return se * c.downlink_layers * c.resource_elements_per_s * c.downlink_fraction / 1e6
