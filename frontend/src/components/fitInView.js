// A tool window opens at a loose spot with a fixed size, so on a short or
// narrow screen its foot could start off screen. Move it in and, when it is
// taller than the screen, shorten it; its body scrolls the rest.
export default function fitInView(el, positionRef, margin = 8) {
  if (!el || el.offsetHeight === 0) return;
  const maxHeight = window.innerHeight - 2 * margin;
  if (el.offsetHeight > maxHeight) el.style.height = `${maxHeight}px`;
  const maxX = Math.max(margin, window.innerWidth - el.offsetWidth - margin);
  const maxY = Math.max(margin, window.innerHeight - el.offsetHeight - margin);
  positionRef.current = {
    x: Math.min(positionRef.current.x, maxX),
    y: Math.min(positionRef.current.y, maxY),
  };
  el.style.left = `${positionRef.current.x}px`;
  el.style.top = `${positionRef.current.y}px`;
}
