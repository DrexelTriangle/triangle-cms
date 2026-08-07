import "trix";

declare global {
  // The attributes an attachment is constructed with. These are what Trix
  // serializes into the figure's data-trix-attachment JSON, so anything added
  // here survives a save/load round trip -- which is how alt text is carried.
  interface TrixAttachmentAttributes {
    url: string;
    href?: string;
    contentType?: string;
    filename?: string;
    filesize?: number;
    width?: number;
    height?: number;
    alt?: string;
    previewable?: boolean;
  }

  interface Window {
    Trix?: {
      config: {
        attachments: {
          preview: {
            // Trix's default is "gallery", which is what makes its gallery
            // filter fuse adjacent images into one block. Nullable so we can
            // turn that off -- see TrixEditor.tsx.
            presentation: string | null;
            caption: {
              name: boolean;
              size: boolean;
            };
          };
        };
      };
      // The attribute allowlist Trix applies to the piece that carries an
      // attachment. Anything not in it is stripped the moment the piece is
      // built, so an attribute has to be added here to survive a re-render.
      AttachmentPiece: { permittedAttributes: string[] };
      Attachment: {
        new (attributes: TrixAttachmentAttributes): TrixAttachment;
        attachmentForFile(file: File): TrixAttachment;
      };
      // Trix re-exports its internal models on the global. HTMLParser is how a
      // Document is built from HTML without going through Editor#loadHTML,
      // which would reset the undo stack.
      HTMLParser: {
        parse(
          html: string,
          options?: { referenceElement?: Element },
        ): { getDocument(): TrixDocument };
      };
    };
  }

  interface TrixDocument {
    getAttachments(): TrixAttachment[];
    locationFromPosition(position: number): { index: number; offset: number };
    getBlockAtIndex(index: number): TrixBlock | undefined;
  }

  interface TrixBlock {
    isEmpty(): boolean;
  }

  // Not part of Trix's documented editor API, but the only way to put an
  // attachment back into its "being edited" state (caption field + attachment
  // toolbar) from code. Trix itself only ever enters that state from a mousedown
  // on the figure, which is no use after we move an attachment and the original
  // element is gone.
  interface TrixComposition {
    editAttachment(
      attachment: TrixAttachment,
      options?: { editCaption?: boolean },
    ): void;
    stopEditingAttachment(): void;
    // The attachment currently in that state, or null. Trix's own name for it.
    editingAttachment: TrixAttachment | null;
    // Replaces the document in place. Unlike Editor#loadHTML / loadSnapshot,
    // this leaves the UndoManager alone, so a caller that has recorded its own
    // undo entry stays undoable.
    setDocument(document: TrixDocument): void;
    insertBlockBreak(): void;
  }

  interface TrixEditorInternal {
    composition: TrixComposition;
    loadHTML(html: string): void;
    recordUndoEntry(
      description: string,
      options?: { context?: unknown; consolidatable?: boolean },
    ): void;
    getDocument(): TrixDocument;
    getSelectedRange(): [number, number];
    setSelectedRange(range: [number, number] | number): void;
    deleteInDirection(direction: "forward" | "backward"): void;
    insertHTML(html: string): void;
    insertAttachment(attachment: TrixAttachment): void;
    insertLineBreak(): void;
    activateAttachment(attachment: TrixAttachment): void;
  }

  interface TrixEditorElement extends HTMLElement {
    editor: TrixEditorInternal;
    value: string;
  }

  interface TrixAttachment {
    id: number;
    file: File | null;
    getAttribute(name: string): unknown;
    getAttributes(): Partial<TrixAttachmentAttributes>;
    setUploadProgress(value: number): void;
    setAttributes(attrs: Partial<TrixAttachmentAttributes>): void;
    remove(): void;
  }

  interface TrixAttachmentAddEvent extends Event {
    attachment: TrixAttachment;
  }

  interface TrixFileAcceptEvent extends Event {
    file: File;
  }

  // Fired while Trix is building the little toolbar that floats over a selected
  // attachment, before it is inserted -- our hook for adding buttons of our own
  // next to Trix's built-in Remove.
  interface TrixAttachmentToolbarEvent extends Event {
    toolbar: HTMLElement;
    attachment: TrixAttachment;
  }
}

declare module "react" {
  namespace JSX {
    interface IntrinsicElements {
      "trix-editor": {
        toolbar?: string;
        className?: string;
        autofocus?: boolean;
        [key: string]: unknown;
      };
      "trix-toolbar": {
        id?: string;
        children?: import("react").ReactNode;
        [key: string]: unknown;
      };
    }
  }
}
