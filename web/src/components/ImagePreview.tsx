import { useEffect, useState } from "react";
import { forge } from "../bridge";

// ImagePreview renders a downscaled copy of an image on disk. Used for what
// the user attached and for what a tool pulled in, so both are visible rather
// than being taken on trust.
export function ImagePreview({ path, alt, small }: { path: string; alt?: string; small?: boolean }) {
  const [src, setSrc] = useState("");
  const [failed, setFailed] = useState(false);

  useEffect(() => {
    let live = true;
    void forge
      .imagePreview(path)
      .then((data) => live && setSrc(data))
      .catch(() => live && setFailed(true));
    return () => {
      live = false;
    };
  }, [path]);

  if (failed) return <span className="img-failed">preview unavailable</span>;
  if (!src) return <span className={`img-loading ${small ? "small" : ""}`} />;
  return <img className={`img-preview ${small ? "small" : ""}`} src={src} alt={alt || path} title={path} />;
}
