import React, { useMemo, useState } from 'react'
import { createRoot } from 'react-dom/client'
import './styles.css'

const money = value => new Intl.NumberFormat('fa-IR').format(value || 0)
const hour = value => value ? new Date(value).toLocaleTimeString('fa-IR', { hour: '2-digit', minute: '2-digit' }) : '--:--'
const modeLabels = { 'best-value': 'بهترین انتخاب', cheapest: 'ارزان‌ترین', fastest: 'سریع‌ترین', baggage: 'بار بیشتر' }
const reasonLabels = { lowest_price: 'کمترین قیمت', shortest_duration: 'کوتاه‌ترین زمان', best_baggage: 'بهترین مقدار بار', nonstop_best_value: 'بهترین گزینه مستقیم', best_value: 'بهترین توازن قیمت و شرایط' }

function App() {
  const [form, setForm] = useState({ origin: 'IKA', destination: 'MHD', date: '' })
  const [offers, setOffers] = useState([]); const [loading, setLoading] = useState(false); const [error, setError] = useState('')
  const [mode, setMode] = useState('best-value'); const [filters, setFilters] = useState({ maxStops: '', airline: '' }); const [compare, setCompare] = useState([])
  const update = event => setForm({ ...form, [event.target.name]: event.target.type === 'date' ? event.target.value : event.target.value.toUpperCase() })
  async function search(event) {
    event.preventDefault(); setLoading(true); setError('')
    try { const params = new URLSearchParams({ ...form, mode }); if (filters.maxStops) params.set('max_stops', filters.maxStops); const response = await fetch(`/api/v1/flights/ranked?${params}`); const body = await response.json(); if (!response.ok) throw new Error(body?.error?.message || 'جست‌وجو ناموفق بود'); setOffers(body.data || []); setCompare([]) }
    catch (err) { setError(err.message); setOffers([]) } finally { setLoading(false) }
  }
  const visible = useMemo(() => offers.filter(item => { const f = item.flight || item; const airlineOK = !filters.airline || (f.airline_code || '').toLowerCase().includes(filters.airline.toLowerCase()); const stopsOK = filters.maxStops === '' || Number(f.stops || 0) <= Number(filters.maxStops); return airlineOK && stopsOK }), [offers, filters.airline, filters.maxStops])
  const toggleCompare = item => { const f = item.flight || item; setCompare(current => current.some(x => x.id === f.id) ? current.filter(x => x.id !== f.id) : current.length < 3 ? [...current, f] : current) }
  return <main dir="rtl">
    <header className="topbar"><div className="brand"><span className="brand-mark">✦</span><span>ترب<span className="accent">پرواز</span></span></div><span className="status"><i /> قیمت‌های زنده و قابل بررسی</span></header>
    <section className="hero"><p className="eyebrow">جست‌وجوی هوشمند پرواز</p><h1>پروازی که<br /><em>به کارتان می‌آید.</em></h1><p className="lede">قیمت و شرایط پروازهای چندین ارائه‌دهنده را مقایسه کنید و دلیل پیشنهاد هر نتیجه را ببینید.</p>
      <form className="search-card" onSubmit={search}><label>مبدأ<input name="origin" value={form.origin} onChange={update} maxLength="8" placeholder="IKA" required /></label><span className="swap">⇄</span><label>مقصد<input name="destination" value={form.destination} onChange={update} maxLength="8" placeholder="MHD" required /></label><label>تاریخ<input name="date" value={form.date} onChange={update} type="date" required /></label><button disabled={loading}>{loading ? 'در حال جست‌وجو…' : 'جست‌وجوی پرواز'} <span>↗</span></button></form>
      <div className="mode-tabs">{Object.entries(modeLabels).map(([key, label]) => <button type="button" className={mode === key ? 'active' : ''} onClick={() => setMode(key)} key={key}>{label}</button>)}</div>
    </section>
    <section className="results"><div className="results-head"><div><p className="eyebrow">نتایج شما</p><h2>{visible.length ? `${new Intl.NumberFormat('fa-IR').format(visible.length)} گزینه` : 'آماده جست‌وجو'}</h2></div>{visible.length > 0 && <span className="sorted">{modeLabels[mode]}</span>}</div>
      {offers.length > 0 && <div className="filters"><label>حداکثر توقف<select value={filters.maxStops} onChange={e => setFilters({ ...filters, maxStops: e.target.value })}><option value="">همه</option><option value="0">مستقیم</option><option value="1">یک توقف</option></select></label><label>ایرلاین<input value={filters.airline} onChange={e => setFilters({ ...filters, airline: e.target.value })} placeholder="کد ایرلاین" /></label><span className="hint">حداکثر ۳ پرواز را برای مقایسه انتخاب کنید</span></div>}
      {error && <div className="error">خطا در دریافت اطلاعات: {error}</div>}
      {!loading && !error && offers.length === 0 && <div className="empty"><span>✈</span><p>مبدأ، مقصد و تاریخ را وارد کنید.</p></div>}
      <div className="offers">{visible.map((item, index) => { const f = item.flight || item; const selected = compare.some(x => x.id === f.id); return <article className={`offer ${selected ? 'selected' : ''}`} key={`${f.id || index}-${f.source || ''}`}><div className="rank">{item.rank ? <><small>رتبه</small><strong>#{new Intl.NumberFormat('fa-IR').format(item.rank)}</strong></> : <span>✈</span>}</div><div className="airline"><strong>{f.airline_name || f.airline_code || 'ایرلاین'}</strong><span>{f.flight_number || '—'} · {f.source || 'ارائه‌دهنده'}</span></div><div className="route"><strong>{hour(f.depart_at)} <b>→</b> {hour(f.arrive_at)}</strong><span>{f.origin} <b>→</b> {f.destination} · {f.duration_min ? `${f.duration_min} دقیقه` : 'مدت نامشخص'}</span></div><div className="details"><span>{reasonLabels[item.recommendation] || 'پیشنهاد منتخب'}</span><span>{f.baggage_label || (f.baggage_kg ? `${f.baggage_kg} کیلو بار` : 'بار نامشخص')} · {f.stops ? `${f.stops} توقف` : 'مستقیم'}</span><span className={item.verification_status === 'verified' ? 'verified' : 'stale'}>{item.verification_status === 'verified' ? '✓ تأییدشده' : 'نیازمند بررسی'} · {item.freshness_seconds ? `${Math.max(1, Math.round(item.freshness_seconds / 60))} دقیقه قبل` : 'تازه'}</span></div><div className="price"><small>{item.deal_label === 'great_deal' ? 'پیشنهاد عالی' : item.deal_label === 'good_deal' ? 'قیمت خوب' : 'قیمت'}</small><strong>{money(f.price_toman)}</strong><span>تومان {item.price_delta_percent ? `(${Math.round(item.price_delta_percent)}٪ نسبت به معمول)` : ''}</span></div><div className="offer-actions"><button type="button" onClick={() => toggleCompare(item)}>{selected ? '✓ انتخاب شد' : 'مقایسه'}</button>{item.provider_url && <a href={item.provider_url} target="_blank" rel="noreferrer">مشاهده در {f.source}</a>}</div></article> })}</div>
      {compare.length > 0 && <div className="compare"><strong>مقایسه {new Intl.NumberFormat('fa-IR').format(compare.length)} پرواز</strong>{compare.map(f => <span key={f.id}>{f.airline_name || f.airline_code} · {money(f.price_toman)} تومان · {hour(f.depart_at)}</span>)}<button onClick={() => setCompare([])}>پاک کردن</button></div>}
    </section><footer>ترب پرواز <span>·</span> قیمت‌ها از چند ارائه‌دهنده جمع‌آوری و تازگی آن‌ها بررسی می‌شود</footer>
  </main>
}
createRoot(document.getElementById('root')).render(<App />)
