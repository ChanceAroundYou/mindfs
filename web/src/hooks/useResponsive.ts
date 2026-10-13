import { useEffect, useState } from "react";
import { MOBILE_BREAKPOINT } from "../shared/actionBarUtils";

export function useResponsive() {
  const [isMobile, setIsMobile] = useState(false);

  useEffect(() => {
    const checkSize = () => setIsMobile(window.innerWidth < MOBILE_BREAKPOINT);
    checkSize();
    window.addEventListener("resize", checkSize);
    return () => window.removeEventListener("resize", checkSize);
  }, []);

  return { isMobile };
}
