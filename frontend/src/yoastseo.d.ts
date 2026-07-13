// yoastseo ships without usable type declarations (its package "types" field is
// a bare string, not a path), so we declare just the surface we consume.
declare module "yoastseo" {
  export interface PaperAttributes {
    keyword?: string
    synonyms?: string
    description?: string
    title?: string
    titleWidth?: number
    slug?: string
    locale?: string
    permalink?: string
    textTitle?: string
  }

  export class Paper {
    constructor(text: string, attributes?: PaperAttributes)
  }

  export class AnalysisWorkerWrapper {
    constructor(worker: Worker)
    initialize(configuration: Record<string, unknown>): Promise<unknown>
    analyze(paper: Paper): Promise<{ result: AnalysisResults }>
  }
}

declare module "yoastseo/build/worker/AnalysisWebWorker" {
  export default class AnalysisWebWorker {
    constructor(scope: unknown, researcher: unknown)
    register(): void
  }
}

declare module "yoastseo/build/languageProcessing/languages/en/Researcher" {
  export default class Researcher {
    constructor(paper?: unknown)
  }
}

// Serialized assessment result shape returned across the worker boundary.
interface SerializedAssessmentResult {
  identifier: string
  score: number
  text: string
}

interface AssessorResults {
  score: number
  results: SerializedAssessmentResult[]
}

interface AnalysisResults {
  readability: AssessorResults
  seo: Record<string, AssessorResults>
}
