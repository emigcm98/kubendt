"""Ray-traced coverage maps of a zone: path gain per sector on a grid, and the streets they serve."""
from dataclasses import dataclass, field
from pathlib import Path

import numpy as np


@dataclass
class RadioMaps:
    """Path gain of every sector on a regular grid, with the building heights and the walkable cells."""
    path_gain: np.ndarray
    x: np.ndarray
    y: np.ndarray
    height: np.ndarray
    outdoor: np.ndarray
    sector_position: np.ndarray
    sector_site: np.ndarray
    config: dict = field(default_factory=dict)

    @property
    def num_sectors(self):
        return self.path_gain.shape[0]

    @property
    def shape(self):
        return self.outdoor.shape

    def cell_of(self, xy):
        """Flat cell index of positions [..., 2]."""
        cell = self.x[1] - self.x[0]
        ix = np.clip(np.rint((xy[..., 0] - self.x[0]) / cell).astype(np.int64), 0, len(self.x) - 1)
        iy = np.clip(np.rint((xy[..., 1] - self.y[0]) / cell).astype(np.int64), 0, len(self.y) - 1)
        return iy * len(self.x) + ix

    def xy_of(self, cell):
        """Cell-centre coordinates of flat cell indices."""
        return np.stack([self.x[cell % len(self.x)], self.y[cell // len(self.x)]], axis=-1)

    def gain_db(self, cell):
        """Path gain [..., sectors] in dB at flat cell indices [...]."""
        flat = self.path_gain.reshape(self.num_sectors, -1)
        return 10.0 * np.log10(np.maximum(flat[:, cell], 1e-30)).transpose(*range(1, cell.ndim + 1), 0)

    def save(self, path):
        """Write the maps to a compressed .npz file."""
        Path(path).parent.mkdir(parents=True, exist_ok=True)
        np.savez_compressed(path, path_gain=self.path_gain, x=self.x, y=self.y, height=self.height, outdoor=self.outdoor,
                            sector_position=self.sector_position, sector_site=self.sector_site, config=np.array(repr(self.config)))

    @classmethod
    def load(cls, path):
        """Read the maps written by save()."""
        z = np.load(path)
        return cls(z["path_gain"], z["x"], z["y"], z["height"], z["outdoor"], z["sector_position"], z["sector_site"],
                   {"repr": str(z["config"])})


def largest_component(mask):
    """The largest 8-connected region of a boolean grid."""
    from scipy import ndimage
    labels, count = ndimage.label(mask, structure=np.ones((3, 3)))
    if count <= 1:
        return mask
    sizes = ndimage.sum(mask, labels, index=np.arange(1, count + 1))
    return labels == (1 + int(np.argmax(sizes)))
