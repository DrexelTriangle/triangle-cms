
import '../App.css'
import logo from '../assets/logo.png'

function Header() {

    return (
        <nav className="header">
            <img src={logo} alt="Logo" className="header-logo" />
        </nav>
    )

}
export default Header