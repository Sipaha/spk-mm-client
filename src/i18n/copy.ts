import ru from './ru.json'; import en from './en.json'; import zh from './zh.json'; import es from './es.json'; import de from './de.json'; import fr from './fr.json'; import pt from './pt.json'; import ja from './ja.json';
export type Lang = 'ru'|'en'|'zh'|'es'|'de'|'fr'|'pt'|'ja';
export const copy = (lang: Lang) => ({ru,en,zh,es,de,fr,pt,ja})[lang];
