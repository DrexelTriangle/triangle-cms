import { useCallback, useEffect, useRef, useState } from "react"
import { AnalysisWorkerWrapper, Paper } from "yoastseo"

// Inputs for a single analysis run. `titleWidth` is the rendered pixel width of
// the SEO title (measured with canvas in SeoAnalysis); without it Yoast's
// "SEO title width" assessment always fails.
export type YoastInput = {
  text: string
  keyphrase: string
  title: string
  titleWidth: number
  description: string
  slug: string
  permalink: string
}

export type YoastAnalysis = {
  results: AnalysisResults | null
  isReady: boolean
  isAnalyzing: boolean
  /** True when inputs changed since the last completed run. */
  isStale: boolean
  error: string | null
  /** Run analysis on demand against the current inputs. */
  analyze: () => void
}

function inputToPaper(input: YoastInput): Paper {
  return new Paper(input.text, {
    keyword: input.keyphrase,
    title: input.title,
    titleWidth: input.titleWidth,
    description: input.description,
    slug: input.slug,
    permalink: input.permalink,
    locale: "en_US",
  })
}

// Analysis is on-demand rather than per-keystroke: the worker boots, runs once
// automatically when there is content, then re-runs only when `analyze()` is
// called (wired to the Refresh button). `isStale` tracks drift in between.
export function useYoastAnalysis(input: YoastInput): YoastAnalysis {
  const wrapperRef = useRef<AnalysisWorkerWrapper | null>(null)
  const workerRef = useRef<Worker | null>(null)
  // Latest inputs, so analyze() always reads current values without being
  // re-created (and re-triggering effects) on every keystroke.
  const inputRef = useRef(input)
  inputRef.current = input
  const didAutoRunRef = useRef(false)

  const [isReady, setIsReady] = useState(false)
  const [isAnalyzing, setIsAnalyzing] = useState(false)
  const [isStale, setIsStale] = useState(false)
  const [results, setResults] = useState<AnalysisResults | null>(null)
  const [error, setError] = useState<string | null>(null)

  const analyze = useCallback(() => {
    const wrapper = wrapperRef.current
    if (!wrapper) return
    setIsAnalyzing(true)
    setError(null)
    wrapper
      .analyze(inputToPaper(inputRef.current))
      .then((payload) => {
        setResults(payload.result)
        setIsStale(false)
      })
      .catch((err: unknown) => {
        setError(err instanceof Error ? err.message : "SEO analysis failed.")
      })
      .finally(() => setIsAnalyzing(false))
  }, [])

  // Boot the worker once.
  useEffect(() => {
    let cancelled = false
    const worker = new Worker(new URL("./yoast.worker.ts", import.meta.url), {
      type: "module",
    })
    const wrapper = new AnalysisWorkerWrapper(worker)
    workerRef.current = worker
    wrapperRef.current = wrapper

    wrapper
      .initialize({ locale: "en_US" })
      .then(() => {
        if (!cancelled) setIsReady(true)
      })
      .catch((err: unknown) => {
        if (!cancelled) {
          setError(err instanceof Error ? err.message : "Could not load SEO analysis.")
        }
      })

    return () => {
      cancelled = true
      wrapperRef.current = null
      workerRef.current = null
      // AnalysisWorkerWrapper has no terminate(); kill the underlying Worker.
      worker.terminate()
    }
  }, [])

  // One automatic run once the worker is ready and there is something to score.
  useEffect(() => {
    if (isReady && !didAutoRunRef.current && input.text.trim() !== "") {
      didAutoRunRef.current = true
      analyze()
    }
  }, [isReady, input.text, analyze])

  // Flag drift between runs so the UI can prompt a refresh.
  useEffect(() => {
    if (results !== null) setIsStale(true)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [input.text, input.keyphrase, input.title, input.titleWidth, input.description, input.slug, input.permalink])

  return { results, isReady, isAnalyzing, isStale, error, analyze }
}
