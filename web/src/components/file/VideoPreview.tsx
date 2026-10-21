import React, { memo, useEffect, useState } from "react";
import { useRawAssetUrl } from "../../hooks/useRawAssetUrl";
import { useI18n } from "../../i18n/index";

type VideoPreviewProps = {
  path: string;
  root?: string;
  /** unsupported = 是视频但浏览器解不了（.avi/.wmv…），只给下载入口。 */
  kind: "playable" | "unsupported";
};

/**
 * 视频预览：<video> 直连后端的路径式原始资源端点。
 *
 * 直连而不是先取成 blob：后端支持 Range（http.ServeContent），
 * 边下边播、进度条可拖，大文件也不占浏览器内存。
 */
function VideoPreviewInner({ path, root, kind }: VideoPreviewProps) {
  const { t } = useI18n();
  const url = useRawAssetUrl(root, path);
  const [failed, setFailed] = useState(false);

  useEffect(() => {
    setFailed(false);
  }, [url]);

  if (kind === "unsupported" || !url) {
    return <div className="document-preview-message">{t("fileViewer.videoUnsupported")}</div>;
  }
  if (failed) {
    return <div className="document-preview-message">{t("fileViewer.videoUnsupported")}</div>;
  }
  return (
    <div className="document-preview-video">
      <video
        controls
        preload="metadata"
        src={url}
        onError={() => setFailed(true)}
      />
    </div>
  );
}

export const VideoPreview = memo(VideoPreviewInner, (prev, next) => (
  prev.path === next.path && prev.root === next.root && prev.kind === next.kind
));
