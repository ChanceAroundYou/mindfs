export type UploadProgress = {
  loaded: number;
  total: number;
  percent: number;
  speedBps: number;
};

/**
 * 待发送附件的最小形状：新建任务面板的 TaskInlineAttachment 与对话输入框的
 * PendingAttachment 都结构化兼容它。
 */
export type ComposerAttachment = {
  id: string;
  file: File;
  previewUrl?: string;
  isImage: boolean;
};
