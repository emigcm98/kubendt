package drivers_meta

// KnownImagesProvider lists the images a driver was tried with, by tag or by
// digest. A node on another image deploys with an `image_untested` warning,
// never a refusal; a driver that lists none takes any image, which is what a
// host does.
type KnownImagesProvider interface {
	KnownImages() []string
}
